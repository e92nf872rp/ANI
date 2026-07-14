import { createFileRoute } from '@tanstack/react-router'
import { Input, Button, Card, Tag } from 'tdesign-react'
import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { api } from '@/api/client'

type KBSource = {
  file_name?: string
  page?: string | number
}

type ChatMessage = {
  role: 'user' | 'assistant'
  content: string
  sources?: KBSource[]
}

export const Route = createFileRoute('/kb/$kbId/chat')({
  component: KBChat,
})

function KBChat() {
  const { kbId } = Route.useParams()
  const [question, setQuestion] = useState('')
  const [messages, setMessages] = useState<ChatMessage[]>([])

  const queryMutation = useMutation({
    mutationFn: (q: string) =>
      api.POST('/knowledge-bases/{kb_id}/query', {
        params: { path: { kb_id: kbId } },
        body: { question: q, idempotency_key: `chat-${Date.now()}` },
      }).then(({ data }) => data),
    onSuccess: (data) => {
      setMessages(prev => [
        ...prev,
        { role: 'assistant', content: data?.answer ?? '', sources: data?.sources as KBSource[] | undefined },
      ])
    },
  })

  const handleSend = () => {
    if (!question.trim()) return
    setMessages(prev => [...prev, { role: 'user', content: question }])
    queryMutation.mutate(question)
    setQuestion('')
  }

  return (
    <div style={{ maxWidth: 800 }}>
      <h2>知识库问答</h2>
      <div style={{ minHeight: 400, border: '1px solid var(--td-component-border)', borderRadius: 8, padding: 16, marginBottom: 16 }}>
        {messages.map((msg, i) => (
          <div key={i} style={{ marginBottom: 12, textAlign: msg.role === 'user' ? 'right' : 'left' }}>
            <Card style={{ display: 'inline-block', maxWidth: '80%', background: msg.role === 'user' ? 'var(--td-brand-color-light)' : '#f5f5f5' }}>
              <p>{msg.content}</p>
              {msg.sources?.map((s, si) => (
                <Tag key={si} size="small" theme="default" style={{ marginRight: 4 }}>
                  📄 {s.file_name} p.{s.page}
                </Tag>
              ))}
            </Card>
          </div>
        ))}
        {queryMutation.isPending && <p style={{ color: '#aaa' }}>AI 正在思考…</p>}
      </div>
      <div style={{ display: 'flex', gap: 8 }}>
        <Input
          value={question}
          onChange={val => setQuestion(val as string)}
          onEnter={handleSend}
          placeholder="输入问题，按 Enter 发送"
          style={{ flex: 1 }}
        />
        <Button onClick={handleSend} loading={queryMutation.isPending}>发送</Button>
      </div>
    </div>
  )
}
