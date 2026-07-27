import { useEffect, useRef, useState } from 'react'
import { Layout, Nav, Button, Tag, Typography, Card, Input, Toast, Banner } from '@douyinfe/semi-ui'
import { api, StateResponse } from './api'
import { hasKey, setKey, onAuthChange } from './session'
import { readPanelAuth } from './panelAuth'
import RulesPanel from './panels/RulesPanel'
import KeysPanel from './panels/KeysPanel'
import PreviewPanel from './panels/PreviewPanel'

const { Header, Content } = Layout

export default function App() {
  const [authed, setAuthed] = useState(hasKey())
  const [state, setState] = useState<StateResponse | null>(null)
  const [inputKey, setInputKey] = useState('')
  const [tab, setTab] = useState('rules')
  // 面板 key 只自动尝试一次。否则 401 → clearKey → authed=false → 再次读到同一个
  // 失效 key → 重新登录 → 401……形成无限重试，用户也回不到登录表单。
  const panelAuthTried = useRef(false)

  useEffect(() => {
    return onAuthChange((ok) => {
      setAuthed(ok)
      if (!ok) {
        setState(null)
        setInputKey('')
      }
    })
  }, [])

  useEffect(() => {
    if (!authed && !panelAuthTried.current) {
      panelAuthTried.current = true
      const panel = readPanelAuth()
      if (panel) {
        setKey(panel.managementKey)
        setAuthed(true)
      }
    }
  }, [authed])

  useEffect(() => {
    if (authed) {
      api.getState().then(setState).catch((e: Error) => Toast.error(e.message))
    }
  }, [authed])

  const login = () => {
    const key = inputKey.trim()
    if (!key) return
    setKey(key)
    setAuthed(true)
  }

  if (!authed) {
    return (
      <Layout style={{ minHeight: '100vh' }}>
        <Content style={{ display: 'flex', justifyContent: 'center', alignItems: 'center' }}>
          <Card title="Model Mapper Plus 登录" style={{ width: 380 }}>
            <Input
              mode="password"
              placeholder="CPA management key"
              value={inputKey}
              onChange={setInputKey}
              onEnterPress={login}
            />
            <Button theme="solid" style={{ marginTop: 12 }} block
              disabled={!inputKey.trim()} onClick={login}>登录</Button>
          </Card>
        </Content>
      </Layout>
    )
  }

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Header>
        <Nav mode="horizontal" header={{ text: 'Model Mapper Plus' }}
          footer={
            <>
              <Tag color={state ? 'green' : 'grey'}>{state ? '已连接' : '加载中'}</Tag>
              {state && (
                <Typography.Text size="small" style={{ marginLeft: 8 }}>
                  {state.persisted
                    ? `state 已保存 ${state.updated_at ?? ''}`
                    : '当前为 YAML 配置（首次保存后接管）'}
                  {state.state_file ? ` · ${state.state_file}` : ''}
                  {state.plugin_version ? ` · v${state.plugin_version}` : ''}
                </Typography.Text>
              )}
            </>
          }
        />
      </Header>
      <Content>
        {state?.load_error && (
          <Banner type="danger" style={{ margin: 16 }}
            title="state_file 未被采用，当前回退到 YAML 配置"
            description={
              <span>
                {state.load_error}
                <br />
                原文件已重命名为 <Typography.Text code>{state.state_file}.corrupt</Typography.Text>，
                其中的 key 绑定仍可人工恢复。此处保存将写入新的 state_file。
              </span>
            }
          />
        )}
        <Nav mode="horizontal" selectedKey={tab} style={{ marginBottom: 8 }}
          onSelect={(data) => setTab(String(data.itemKey))}
          items={[
            { itemKey: 'rules', text: '规则管理' },
            { itemKey: 'keys', text: 'Key 绑定' },
            { itemKey: 'preview', text: '规则试跑' },
          ]} />
        {tab === 'rules' && state && <RulesPanel state={state} onSaved={setState} />}
        {tab === 'keys' && state && <KeysPanel state={state} onSaved={setState} />}
        {tab === 'preview' && <PreviewPanel />}
      </Content>
    </Layout>
  )
}
