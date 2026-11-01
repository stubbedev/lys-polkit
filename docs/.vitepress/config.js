import { lysConfig } from 'lys-docs-theme/config'

export default lysConfig({
  name: 'lys-polkit',
  description: "The lys desktop's polkit authentication agent.",
  nav: [{ text: 'Guide', link: '/guide/getting-started' }],
  sidebar: [
    {
      text: 'Guide',
      items: [
        { text: 'Getting started', link: '/guide/getting-started' },
        { text: 'Install', link: '/guide/install' },
        { text: 'Configuration', link: '/guide/configuration' },
        { text: 'How it works', link: '/guide/how-it-works' },
        { text: 'Development', link: '/guide/development' },
      ],
    },
  ],
})
