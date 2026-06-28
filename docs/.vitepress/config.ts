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

      // Inline code may legitimately contain `{{...}}` (Go template syntax in
      // our docs + the auto-generated changelog). Vue/VitePress otherwise
      // parses those braces as a template interpolation and the build dies with
      // "Error parsing JavaScript expression". Wrap every inline-code span in
      // v-pre so Vue treats its contents verbatim. Applies to ALL pages, so
      // any future page (or a Gemini-generated changelog entry) is safe by
      // default — no per-file escaping needed.
      const defaultCodeInline =
        md.renderer.rules.code_inline ||
        ((tokens, idx, options, _env, self) =>
          `<code${self.renderAttrs(tokens[idx])}>${md.utils.escapeHtml(
            tokens[idx].content,
          )}</code>`)
      md.renderer.rules.code_inline = (tokens, idx, options, env, self) => {
        const rendered = defaultCodeInline(tokens, idx, options, env, self)
        if (!tokens[idx].content.includes('{{')) return rendered
        return rendered.replace(/^<code/, '<code v-pre')
      }
    },
  },

  themeConfig: {
    logo: '/logo.svg',

    nav: [
      { text: 'Wick Agent', link: '/guide/agents-only' },
      { text: 'Wick Framework', link: '/guide/getting-started' },
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
          { text: 'Custom Connectors', link: '/guide/custom-connectors' },
          { text: 'Connector Plugins', link: '/guide/connector-plugins' },
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
        text: 'Built-in Connectors',
        items: [
          { text: 'Overview', link: '/connectors/' },
          { text: 'HTTP / REST', link: '/connectors/httprest' },
          { text: 'GitHub', link: '/connectors/github' },
          { text: 'Bitbucket', link: '/connectors/bitbucket' },
          { text: 'Slack', link: '/connectors/slack' },
          { text: 'Google Workspace', link: '/connectors/googleworkspace' },
          { text: 'Phoenix', link: '/connectors/phoenix' },
          { text: 'Wick Manager', link: '/connectors/wickmanager' },
          { text: 'Workflow', link: '/connectors/workflow' },
          { text: 'Notifications', link: '/connectors/notifications' },
          { text: 'CRUD CRUD (lab)', link: '/connectors/crudcrud' },
        ],
      },
      {
        text: 'AI Agents',
        items: [
          { text: 'Agent Host Only (no Go needed)', link: '/guide/agents-only' },
          {
            text: 'Install',
            collapsed: false,
            items: [
              { text: 'Desktop Tray', link: '/guide/desktop-tray' },
              { text: 'Headless Server', link: '/guide/headless' },
              { text: 'Docker', link: '/guide/docker' },
              { text: 'Termux / Android', link: '/guide/termux-android' },
            ],
          },
          { text: 'Overview', link: '/guide/agents' },
          { text: 'Projects', link: '/guide/agents/projects' },
          { text: 'Providers', link: '/guide/agents/providers' },
          { text: 'Channels (Slack / Telegram / Web)', link: '/guide/agents/channels' },
          { text: 'Pool & Sessions', link: '/guide/agents/pool' },
          { text: 'Source Control', link: '/guide/agents/source-control' },
          { text: 'Command Gate', link: '/guide/command-gate' },
        ],
      },
      {
        text: 'Workflows',
        items: [
          { text: 'Overview', link: '/workflow/' },
          { text: 'Nodes', link: '/workflow/nodes' },
          { text: 'Triggers', link: '/workflow/triggers' },
          { text: 'Canvas editor', link: '/workflow/canvas' },
          { text: 'MCP authoring', link: '/workflow/mcp' },
          { text: 'Run state', link: '/workflow/state' },
        ],
      },
      {
        text: 'Workflow nodes',
        collapsed: true,
        items: [
          { text: 'agent', link: '/workflow/nodes/agent' },
          { text: 'branch', link: '/workflow/nodes/branch' },
          { text: 'channel', link: '/workflow/nodes/channel' },
          { text: 'classify', link: '/workflow/nodes/classify' },
          { text: 'connector', link: '/workflow/nodes/connector' },
          { text: 'datatable_*', link: '/workflow/nodes/datatable' },
          { text: 'db_query', link: '/workflow/nodes/db-query' },
          { text: 'end', link: '/workflow/nodes/end' },
          { text: 'go_script', link: '/workflow/nodes/go-script' },
          { text: 'http', link: '/workflow/nodes/http' },
          { text: 'session_init', link: '/workflow/nodes/session_init' },
          { text: 'shell', link: '/workflow/nodes/shell' },
          { text: 'switch', link: '/workflow/nodes/switch' },
          { text: 'transform', link: '/workflow/nodes/transform' },
        ],
      },
      {
        text: 'Reference',
        items: [
          { text: 'CLI', link: '/reference/cli' },
          { text: 'App CLI', link: '/reference/app-cli' },
          { text: 'wick.yml', link: '/reference/wick-yml' },
          { text: 'wick build', link: '/reference/build' },
          { text: 'Environment Variables', link: '/reference/env-vars' },
          { text: 'Connector API', link: '/reference/connector-api' },
          { text: 'Config Tags', link: '/reference/config-tags' },
          { text: 'Encrypted Fields', link: '/reference/encrypted-fields' },
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
