package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/Masterminds/semver/v3"
	"log"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v60/github"
)

const (
	authTokenEnv    = "GITHUB_PUBLIC_REPO_READ_TOKEN"
	cliAppTopic     = "cli"
	libraryAppTopic = "library"
)

var (
	username  = flag.String("username", "", "GitHub username")
	hostname  = flag.String("hostname", "", "Go package index hostname")
	outPath   = flag.String("out", "repos.json", "Output file path")
	forceAuth = flag.Bool("force-authenticated", false, "You must set the "+authTokenEnv+" env var if you used this flag")
)

type Repo struct {
	Owner        string            `json:"owner"`
	Name         string            `json:"name"`
	Stars        uint              `json:"stars"`
	Description  string            `json:"desc"`
	GoPackage    string            `json:"go_package"`
	GoInstall    string            `json:"go_install"`
	LatestTag    string            `json:"latest_tag"`
	AlphaRelease bool              `json:"alpha_release"`
	HasCLIApp    bool              `json:"has_cli_app"`
	IsLibrary    bool              `json:"is_library"`
	Packages     map[string]string `json:"packages"`
	MasterBranch string            `json:"master_branch"`
}

type RepoArchive []*Repo

func main() {
	log.SetPrefix("sync-github-repos: ")
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	start := time.Now()

	flag.Parse()
	validateFlags()

	cl := github.NewClient(nil)

	token := os.Getenv(authTokenEnv)
	if token != "" {
		log.Printf("Using auth token from env var: %s", authTokenEnv)
		cl = cl.WithAuthToken(token)
	} else {
		if *forceAuth {
			log.Fatalf("You must set the %s env var if you used the -force-authenticated flag", authTokenEnv)
		}
	}

	ctx := context.Background()
	repos := listRepos(ctx, cl, *username)

	repoByPkg := filterGoRepositories(ctx, cl, *hostname, repos)
	repoArchive := make(RepoArchive, 0, len(repos))
	for pkg, repo := range repoByPkg {
		repoArchive = append(repoArchive, &Repo{
			Owner:        repo.GetOwner().GetLogin(),
			Name:         repo.GetName(),
			Stars:        uint(repo.GetStargazersCount()),
			Description:  repo.GetDescription(),
			GoPackage:    pkg,
			HasCLIApp:    repo.Topics != nil && slices.Contains(repo.Topics, cliAppTopic),
			IsLibrary:    repo.Topics != nil && slices.Contains(repo.Topics, libraryAppTopic),
			MasterBranch: repo.GetDefaultBranch(),
		})
	}

	populateLatestReleases(ctx, cl, repoArchive)
	populatePackages(ctx, cl, *username, repoArchive)

	b, err := json.MarshalIndent(repoArchive, "", "  ")
	must(err, "marshaling JSON")
	must(os.WriteFile(*outPath, b, 0644), "writing JSON to file")

	log.Printf("Done in %v", time.Since(start))
}

func populatePackages(ctx context.Context, cl *github.Client, user string, repos RepoArchive) {
	var allPackages []*github.Package
	opt := &github.PackageListOptions{
		ListOptions: github.ListOptions{PerPage: 10},
		PackageType: github.String("container"),
	}
	for {
		repos, resp, err := cl.Users.ListPackages(ctx, user, opt)
		must(err, "listing packages")
		allPackages = append(allPackages, repos...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	for _, pkg := range allPackages {
		for _, r := range repos {
			if pkg.Repository.GetFullName() == r.Owner+"/"+r.Name {
				if r.Packages == nil {
					r.Packages = make(map[string]string)
				}
				r.Packages[pkg.GetName()] = fmt.Sprintf("ghcr.io/%s/%s:%s", user, pkg.GetName(), r.LatestTag)
			}
		}
	}
}

func populateLatestReleases(ctx context.Context, cl *github.Client, repos RepoArchive) {
	wg := sync.WaitGroup{}
	log.Printf("Fetching latest release and tags for %d repositories", len(repos))
	for _, entry := range repos {
		wg.Add(1)
		go func(repo *Repo) {
			defer wg.Done()

			release, _, _ := cl.Repositories.GetLatestRelease(ctx, repo.Owner, repo.Name)
			tags, _, _ := cl.Repositories.ListTags(ctx, repo.Owner, repo.Name, nil)

			if release == nil && len(tags) > 0 {
				// No release but tags are available, use the latest tag
				latestTag := tags[0].GetName()
				version, err := semver.NewVersion(latestTag)
				if err != nil {
					log.Printf("failed to parse tag with version %q: %v", latestTag, err)
					return
				}
				repo.LatestTag = version.String()
				alpha := version.Prerelease() != "" && strings.Contains(version.Prerelease(), "alpha")
				repo.AlphaRelease = alpha || version.Major() == 0
				log.Printf("using latest tag %s for %s/%s as no release is available", repo.LatestTag, repo.Owner, repo.Name)
			} else if release != nil && len(tags) > 0 {
				// Both release and tags are available, compare dates
				latestTag := tags[0].GetName()
				tagCommit, _, err := cl.Repositories.GetCommit(ctx, repo.Owner, repo.Name, tags[0].GetCommit().GetSHA(), nil)
				if err != nil {
					log.Printf("failed to get commit for tag %q: %v", latestTag, err)
					return
				}
				//log.Printf("latest tag for %s/%s is %s, date: %v", repo.Owner, repo.Name, latestTag, tagCommit.GetCommit().GetCommitter().GetDate())
				//log.Printf("latest release for %s/%s is %s, date: %v", repo.Owner, repo.Name, release.GetTagName(), release.GetPublishedAt().Time)
				if release.GetPublishedAt().Time.After(tagCommit.GetCommit().GetCommitter().GetDate().Time) {
					version, err := semver.NewVersion(release.GetTagName())
					if err != nil {
						log.Printf("failed to parse tag with version %q: %v", release.GetTagName(), err)
						return
					}
					repo.LatestTag = version.String()
					pre := version.Prerelease() != "" && strings.Contains(version.Prerelease(), "alpha")
					repo.AlphaRelease = pre || version.Major() == 0
					log.Printf("using latest release %s for %s/%s as release newer than tag", repo.LatestTag, repo.Owner, repo.Name)
				} else {
					version, err := semver.NewVersion(latestTag)
					if err != nil {
						log.Printf("failed to parse tag with version %q: %v", latestTag, err)
						return
					}
					repo.LatestTag = version.String()
					alpha := version.Prerelease() != "" && strings.Contains(version.Prerelease(), "alpha")
					repo.AlphaRelease = alpha || version.Major() == 0
					log.Printf("using latest tag %s for %s/%s as tag newer than release", repo.LatestTag, repo.Owner, repo.Name)
				}
			} else if release != nil {
				// Only release is available - if done correct, there shouldn't be a release without tags, but it's possible.
				version, err := semver.NewVersion(release.GetTagName())
				if err != nil {
					log.Printf("failed to parse tag with version %q: %v", release.GetTagName(), err)
					return
				}
				repo.LatestTag = version.String()
				pre := version.Prerelease() != "" && strings.Contains(version.Prerelease(), "alpha")
				repo.AlphaRelease = pre || version.Major() == 0
				log.Printf("using latest release %s for %s/%s as no tags available", repo.LatestTag, repo.Owner, repo.Name)
			} else {
				// Neither release nor tags are available
				log.Printf("no release or tags found for %s/%s", repo.Owner, repo.Name)
			}
		}(entry)
	}
	wg.Wait()
}

func filterGoRepositories(ctx context.Context, cl *github.Client, hostname string, repos []*github.Repository) map[string]*github.Repository {
	mutex := sync.Mutex{}
	goRepos := map[string]*github.Repository{}

	prefix := fmt.Sprintf("module %s", hostname)

	wg := sync.WaitGroup{}
	for _, entry := range repos {
		wg.Add(1)
		go func(repo *github.Repository) {
			defer wg.Done()

			content, _, _, err := cl.Repositories.GetContents(ctx, repo.GetOwner().GetLogin(), repo.GetName(), "go.mod", nil)
			var resp *github.ErrorResponse
			if errors.As(err, &resp) {
				if resp.Response.StatusCode == 404 {
					return
				}
			}
			must(err, "getting go.mod file")
			if content == nil {
				return
			}
			if content.Content == nil {
				return
			}
			contentBytes, err := base64.StdEncoding.DecodeString(*content.Content)
			must(err, "decoding go.mod file")

			contentStr := strings.TrimSpace(string(contentBytes))

			if strings.HasPrefix(contentStr, prefix) {
				goPkg := strings.Split(contentStr, "\n")[0][7:]
				goPkg = strings.TrimPrefix(goPkg, hostname+"/")

				mutex.Lock()
				defer mutex.Unlock()

				if _, ok := goRepos[goPkg]; ok {
					log.Fatal("duplicate go package found:", goPkg)
				}

				log.Printf("go.mod file in %s/%s has prefix %s, and Go package is: %s", repo.GetOwner().GetLogin(), repo.GetName(), prefix, goPkg)
				goRepos[goPkg] = repo
			}
		}(entry)
	}
	wg.Wait()
	return goRepos
}

func listRepos(ctx context.Context, cl *github.Client, user string) []*github.Repository {
	var allRepos []*github.Repository
	opt := &github.RepositoryListByUserOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}
	for {
		repos, resp, err := cl.Repositories.ListByUser(ctx, user, opt)
		must(err, "listing repositories")
		allRepos = append(allRepos, repos...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return allRepos
}

func validateFlags() {
	if *username == "" {
		log.Fatal("username is required")
	}
	if *hostname == "" {
		log.Fatal("hostname is required")
	}
}

func must(err error, msg string) {
	if err == nil {
		return
	}
	var rateLimitError *github.RateLimitError
	if errors.As(err, &rateLimitError) {
		err = fmt.Errorf("rate limited: %v", err)
	}
	var abuseRateLimitError *github.AbuseRateLimitError
	if errors.As(err, &abuseRateLimitError) {
		err = fmt.Errorf("abuse rate limited: %v", err)
	}
	log.Fatalf("%s: %v", msg, err)
}
