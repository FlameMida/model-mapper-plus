import { createRoot } from 'react-dom/client'
import '@semi-css'
import './styles.css'
import App from './App'
import { initThemeSync } from './themeSync'

initThemeSync()
createRoot(document.getElementById('root')!).render(<App />)
