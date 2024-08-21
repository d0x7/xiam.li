export default function manifest() {
    return {
      name: 'xiam.li',
      short_name: 'xiam.li',
      description: "Xiam.Li Go Package Index",
      start_url: '/',
      display: 'standalone',
      background_color: '#0c0a09',
      theme_color: '#0c0a09',
      icons: [
        {
          src: '/favicon.ico',
          sizes: 'any',
          type: 'image/x-icon',
        },
      ],
    }
  }
