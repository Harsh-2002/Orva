import { createApp } from 'vue'
import { createPinia } from 'pinia'
import HistoryControls from './HistoryControls.vue'
import '../style.css'
createApp(HistoryControls).use(createPinia()).mount('#app')
