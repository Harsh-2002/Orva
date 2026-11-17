import { createApp } from 'vue'
import { createPinia } from 'pinia'
import ManagedIAM from './ManagedIAM.vue'
import '../style.css'
createApp(ManagedIAM).use(createPinia()).mount('#app')
