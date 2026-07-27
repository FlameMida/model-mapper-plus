// React 19 下 createRoot 仅从 react-dom/client 导出，Semi 的命令式 API
// （Modal.confirm / Toast / Notification）靠它把弹层挂到 document.body，找不到就
// 静默不弹 —— 表现为「点删除没反应」「保存无提示」。这个副作用 import 把 createRoot
// 注入 Semi 全局配置，必须早于任何 Semi 组件的加载（即早于 import App）。
import '@douyinfe/semi-ui/react19-adapter'
import { createRoot } from 'react-dom/client'
import '@semi-css'
import './styles.css'
import App from './App'
import { initThemeSync } from './themeSync'

initThemeSync()
createRoot(document.getElementById('root')!).render(<App />)
