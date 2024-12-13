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
	for _, entry := range repos {
		wg.Add(1)
		go func(repo *Repo) {
			defer wg.Done()

			release, resp, err := cl.Repositories.GetLatestRelease(ctx, repo.Owner, repo.Name)
			if resp.StatusCode == 404 {
				// This repo may have one pre-release
				releases, _, err := cl.Repositories.ListReleases(ctx, repo.Owner, repo.Name, nil)
				must(err, "listing releases")

				if len(releases) == 0 {
					// Couldn't find a release, let's just take the last tag
					tags, _, err := cl.Repositories.ListTags(ctx, repo.Owner, repo.Name, nil)
					must(err, "listing tags")

					// The first tag in the list is the latest
					if len(tags) > 0 {
						latestTag := tags[0].GetName()

						version, err := semver.NewVersion(latestTag)
						if err != nil {
							log.Printf("failed to parse tag with version %q: %v", latestTag, err)
							return
						}

						repo.LatestTag = version.String()
						alpha := version.Prerelease() != "" && strings.Contains(version.Prerelease(), "alpha")
						v0 := version.Major() == 0
						repo.AlphaRelease = alpha || v0
					}
					// TODO: Maybe change this in the same way gitversion dertermines versions; setting v0.0.0 with on tag
					return
				}

				latest := releases[0]
				for _, rel := range releases {
					if rel.GetCreatedAt().After(latest.GetCreatedAt().Time) {
						latest = rel
					}
				}

				release = latest
			} else {
				must(err, "getting latest release")
			}

			if release != nil {
				repo.LatestTag = release.GetTagName()

				pre := release.GetPrerelease()
				v0 := strings.HasPrefix(repo.LatestTag, "v0.")
				repo.AlphaRelease = pre || v0
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
