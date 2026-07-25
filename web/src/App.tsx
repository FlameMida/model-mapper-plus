import { useEffect, useState } from 'react'
import { Layout, Nav, Button, Tag, Typography, Card, Input, Toast } from '@douyinfe/semi-ui'
import { api, StateResponse } from './api'
import { hasKey, setKey } from './session'
import { readPanelAuth } from './panelAuth'
import RulesPanel from './panels/RulesPanel'

const { Header, Content } = Layout

function Placeholder({ name }: { name: string }) {
  return <Card style={{ margin: 16 }}>{name}（任务 7/8/9 实现）</Card>
}

export default function App() {
  const [authed, setAuthed] = useState(hasKey())
  const [state, setState] = useState<StateResponse | null>(null)
  const [inputKey, setInputKey] = useState('')
  const [tab, setTab] = useState('rules')

  useEffect(() => {
    if (!authed) {
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

  if (!authed) {
    return (
      <Layout style={{ minHeight: '100vh' }}>
        <Content style={{ display: 'flex', justifyContent: 'center', alignItems: 'center' }}>
          <Card title="Model Mapper 登录" style={{ width: 380 }}>
            <Input
              mode="password"
              placeholder="CPA management key"
              value={inputKey}
              onChange={setInputKey}
              onEnterPress={() => { setKey(inputKey); setAuthed(true) }}
            />
            <Button theme="solid" style={{ marginTop: 12 }} block
              onClick={() => { setKey(inputKey); setAuthed(true) }}>登录</Button>
          </Card>
        </Content>
      </Layout>
    )
  }

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Header>
        <Nav mode="horizontal" header={{ text: 'Model Mapper' }}
          footer={
            <>
              <Tag color={state ? 'green' : 'grey'}>{state ? '已连接' : '加载中'}</Tag>
              {state && (
                <Typography.Text size="small" style={{ marginLeft: 8 }}>
                  {state.persisted ? `state 已保存 ${state.updated_at ?? ''}` : '当前为 YAML 配置（首次保存后接管）'}
                </Typography.Text>
              )}
            </>
          }
        />
      </Header>
      <Content>
        <Nav mode="horizontal" selectedKey={tab} style={{ marginBottom: 8 }}
          onSelect={(data) => setTab(String(data.itemKey))}
          items={[
            { itemKey: 'rules', text: '规则管理' },
            { itemKey: 'keys', text: 'Key 绑定' },
            { itemKey: 'preview', text: '规则试跑' },
          ]} />
        {tab === 'rules' && state && <RulesPanel state={state} onSaved={setState} />}
        {tab === 'keys' && <Placeholder name="Key 绑定" />}
        {tab === 'preview' && <Placeholder name="规则试跑" />}
      </Content>
    </Layout>
  )
}
