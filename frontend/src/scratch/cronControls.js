import { createApp, defineComponent, h, KeepAlive } from 'vue'
import { createPinia } from 'pinia'
import { createRouter, createWebHistory, RouterLink, RouterView } from 'vue-router'
import CronJobs from '@/views/CronJobs.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import '@/style.css'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/cron-controls.html', component: CronJobs },
    { path: '/other', component: { template: '<p>Scratch navigation destination.</p>' } },
  ],
})
const App = defineComponent({
  setup: () => () => h('main', { class: 'p-4 sm:p-6 max-w-7xl mx-auto' }, [
    h('nav', { class: 'flex gap-6 mb-6 text-sm text-link', 'aria-label': 'Scratch navigation' }, [
      h(RouterLink, { to: '/cron-controls.html' }, () => 'Schedules'),
      h(RouterLink, { to: '/other' }, () => 'Other view'),
    ]),
    h(RouterView, null, { default: ({ Component }) => h(KeepAlive, null, { default: () => Component && h(Component) }) }),
    h(ConfirmDialog),
  ]),
})
createApp(App).use(createPinia()).use(router).mount('#app')
