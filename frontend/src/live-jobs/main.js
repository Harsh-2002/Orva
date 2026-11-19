import { createApp, h } from 'vue'
import { createPinia } from 'pinia'
import Jobs from '@/views/Jobs.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import '@/style.css'

// Scratch-only entry. All data and capabilities come from the authenticated Jobs API.
createApp({
  render: () => h('main', { class: 'p-4 sm:p-8 max-w-[1440px] mx-auto' }, [
    h(Jobs), h(ConfirmDialog),
  ]),
}).use(createPinia()).mount('#app')
