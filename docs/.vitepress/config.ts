import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'Kai',
  description: 'Semantic code intelligence for CI',
  ignoreDeadLinks: true,
  head: [
    ['link', { rel: 'icon', href: '/favicon.ico' }],
  ],
  themeConfig: {
    nav: [
      { text: 'Docs', link: '/' },
      { text: 'Changelog', link: '/changelog' },
      { text: 'GitHub', link: 'https://github.com/kailayerhq/kai' },
      { text: 'kailayer.com', link: 'https://kailayer.com' },
    ],
    sidebar: [
      {
        text: 'Getting Started',
        items: [
          { text: 'Introduction', link: '/' },
          { text: 'CLI Reference', link: '/cli-reference' },
          { text: 'Data Handling', link: '/data-handling' },
        ],
      },
      {
        text: 'Architecture',
        items: [
          { text: 'Architecture Boundary', link: '/architecture-boundary' },
          { text: 'Boundary Spec', link: '/boundary-spec' },
          { text: 'Extension Points', link: '/extension-points' },
        ],
      },
      {
        text: 'Legal',
        items: [
          { text: 'Licensing', link: '/licensing' },
          { text: 'IP Ownership', link: '/ip-ownership' },
          { text: 'Patent Posture', link: '/patent-posture' },
        ],
      },
      {
        text: 'Project',
        items: [
          { text: 'Changelog', link: '/changelog' },
        ],
      },
    ],
    socialLinks: [
      { icon: 'github', link: 'https://github.com/kailayerhq/kai' },
    ],
  },
})
