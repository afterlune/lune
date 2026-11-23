import { createRoot } from 'react-dom/client'
import App from './App'
import WebApp from './workbench'
import { applyTheme, loadTheme } from './theme'
import './styles.css'
import './workbench.css'

void loadTheme().then((theme) => {
  applyTheme(theme)
  createRoot(document.getElementById('root')!).render(window.luneDesktop ? <App initialTheme={theme} /> : <WebApp theme={theme} />)
})
