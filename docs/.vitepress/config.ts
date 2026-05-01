import { defineConfig } from 'vitepress'
import { withMermaid } from 'vitepress-plugin-mermaid'
import llmstxt, { copyOrDownloadAsMarkdownButtons } from 'vitepress-plugin-llms'
import { readFileSync } from 'fs'
import { resolve } from 'path'

const version = readFileSync(resolve(__dirname, '../../VERSION'), 'utf-8').trim()

export default withMermaid(defineConfig({
  title: 'Wick',
  description: 'AI-first framework for building internal tools and background jobs in Go',
  base: '/wick/',

  head: [
    ['link', { rel: 'icon', href: '/wick/favicon.ico' }],
  ],

  vite: {
    plugins: [llmstxt()],
  },

  markdown: {
    config(md) {
      md.use(copyOrDownloadAsMarkdownButtons)
    },
  },

  themeConfig: {
    logo: '/logo.svg',

    nav: [
      { text: 'Guide', link: '/guide/getting-started' },
      { text: 'AI Quickstart', link: '/guide/ai-quickstart' },
      { text: 'Reference', link: '/reference/wick-yml' },
      {
        text: `v${version}`,
        items: [
          { text: 'Changelog', link: '/changelog' },
          { text: 'Contributing', link: '/contributing' },
        ],
      },
      { text: 'GitHub', link: 'https://github.com/yogasw/wick' },
    ],

    sidebar: [
      {
        text: 'Guide',
        items: [
          { text: 'Introduction', link: '/guide/introduction' },
          { text: 'Getting Started', link: '/guide/getting-started' },
          { text: 'AI Quickstart', link: '/guide/ai-quickstart' },
          { text: 'Admin Panel', link: '/guide/admin-panel' },
          { text: 'Glossary', link: '/guide/glossary' },
        ],
      },
      {
        text: 'Modules',
        items: [
          { text: 'Tool Module', link: '/guide/tool-module' },
          { text: 'Background Job', link: '/guide/job-module' },
          { text: 'Connector Module', link: '/guide/connector-module' },
        ],
      },
      {
        text: 'LLM & Auth',
        items: [
          { text: 'MCP for LLMs', link: '/guide/mcp' },
          { text: 'Access Tokens (PAT)', link: '/guide/access-tokens' },
          { text: 'OAuth Connections', link: '/guide/oauth-connections' },
          { text: 'Connector Runs Purge', link: '/guide/connector-runs-purge' },
        ],
      },
      {
        text: 'Reference',
        items: [
          { text: 'CLI', link: '/reference/cli' },
          { text: 'wick.yml', link: '/reference/wick-yml' },
          { text: 'Environment Variables', link: '/reference/env-vars' },
          { text: 'Connector API', link: '/reference/connector-api' },
        ],
      },
      {
        text: 'Project',
        items: [
          { text: 'Changelog', link: '/changelog' },
          { text: 'Contributing', link: '/contributing' },
        ],
      },
    ],

    socialLinks: [
      { icon: 'github', link: 'https://github.com/yogasw/wick' },
    ],

    footer: {},

    search: {
      provider: 'local',
    },
  },
}))
