import { createRoot } from 'react-dom/client'
import App from './App'
import { applyTheme, loadTheme } from './theme'
import './styles.css'

void loadTheme().then((theme) => {
  applyTheme(theme)
  createRoot(document.getElementById('root')!).render(<App initialTheme={theme} />)
})
