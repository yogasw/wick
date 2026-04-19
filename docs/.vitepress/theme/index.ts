import DefaultTheme from 'vitepress/theme'
import HomeInstall from './components/HomeInstall.vue'
import PromptBox from './components/PromptBox.vue'
import Footer from './components/Footer.vue'
import { h } from 'vue'
import type { App } from 'vue'

export default {
  extends: DefaultTheme,
  Layout() {
    return h(DefaultTheme.Layout, null, {
      'home-hero-image': () => h(HomeInstall),
      'layout-bottom': () => h(Footer),
    })
  },
  enhanceApp({ app }: { app: App }) {
    app.component('PromptBox', PromptBox)
  },
}
