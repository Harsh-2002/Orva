import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import './style.css'
import { installEdgeGuard } from './utils/edgeGuard'

// CSS asks for this and not every engine obeys; see utils/edgeGuard.js.
installEdgeGuard()

const app = createApp(App)
const pinia = createPinia()

app.use(pinia)
app.use(router)
app.mount('#app')
