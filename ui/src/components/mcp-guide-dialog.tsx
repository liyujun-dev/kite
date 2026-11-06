import { useState } from 'react'
import { useAuth } from '@/contexts/auth-context'
import { Bot, Check, Copy } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'

import { withSubPath } from '@/lib/subpath'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

interface MCPGuideDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function MCPGuideDialog({ open, onOpenChange }: MCPGuideDialogProps) {
  const { t } = useTranslation()
  const { user } = useAuth()
  const navigate = useNavigate()
  const [copied, setCopied] = useState<string | null>(null)

  const endpoint = `${window.location.origin}${withSubPath('/mcp')}`
  const apiKeyPlaceholder = 'kite<id>-<key>'
  const jsonConfig = JSON.stringify(
    { mcpServers: { kite: { type: 'http', url: endpoint } } },
    null,
    2
  )
  const apiKeyJsonConfig = JSON.stringify(
    {
      mcpServers: {
        kite: {
          type: 'http',
          url: endpoint,
          headers: {
            Authorization: `Bearer ${apiKeyPlaceholder}`,
          },
        },
      },
    },
    null,
    2
  )

  const copy = async (text: string, key: string) => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(key)
      toast.success(t('mcpGuide.copied'))
      window.setTimeout(() => {
        setCopied((current) => (current === key ? null : current))
      }, 2000)
    } catch (error) {
      console.error('Failed to copy MCP guide text', error)
      toast.error(t('common.error'))
    }
  }

  const copyButton = (text: string, key: string) => (
    <Button
      variant="ghost"
      size="icon"
      className="shrink-0"
      onClick={() => copy(text, key)}
      aria-label={t('mcpGuide.copy')}
    >
      {copied === key ? (
        <Check className="h-4 w-4 text-green-600" />
      ) : (
        <Copy className="h-4 w-4" />
      )}
    </Button>
  )

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] max-w-xl overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Bot className="h-5 w-5" />
            {t('mcpGuide.title')}
          </DialogTitle>
          <DialogDescription>{t('mcpGuide.description')}</DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="space-y-1.5">
            <div className="text-xs font-medium text-muted-foreground">
              {t('mcpGuide.endpointLabel')}
            </div>
            <div className="flex items-center gap-2 rounded-md border bg-muted/40 p-2">
              <code className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap text-xs">
                {endpoint}
              </code>
              {copyButton(endpoint, 'endpoint')}
            </div>
          </div>

          <Tabs defaultValue="oauth" className="gap-4">
            <TabsList className="w-full">
              <TabsTrigger value="oauth">
                {t('mcpGuide.tabs.oauth')}
              </TabsTrigger>
              <TabsTrigger value="apiKey">
                {t('mcpGuide.tabs.apiKey')}
              </TabsTrigger>
            </TabsList>

            <TabsContent value="oauth" className="space-y-4">
              <div className="space-y-1.5">
                <div className="text-xs font-medium text-muted-foreground">
                  {t('mcpGuide.jsonLabel')}
                </div>
                <div className="flex items-start gap-2 rounded-md border bg-muted/40 p-2">
                  <pre className="min-w-0 flex-1 overflow-x-auto text-xs">
                    <code>{jsonConfig}</code>
                  </pre>
                  {copyButton(jsonConfig, 'json')}
                </div>
              </div>

              <p className="text-xs text-muted-foreground">
                {t('mcpGuide.hint')}
              </p>
            </TabsContent>

            <TabsContent value="apiKey" className="space-y-4">
              <p className="text-xs text-muted-foreground">
                {t('mcpGuide.apiKeyHint')}
              </p>

              <div className="space-y-1.5">
                <div className="text-xs font-medium text-muted-foreground">
                  {t('mcpGuide.jsonLabel')}
                </div>
                <div className="flex items-start gap-2 rounded-md border bg-muted/40 p-2">
                  <pre className="min-w-0 flex-1 overflow-x-auto text-xs">
                    <code>{apiKeyJsonConfig}</code>
                  </pre>
                  {copyButton(apiKeyJsonConfig, 'apiKeyJson')}
                </div>
              </div>

              {user?.isAdmin() && (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    onOpenChange(false)
                    navigate('/settings?tab=apikeys')
                  }}
                >
                  {t('mcpGuide.openApiKeys')}
                </Button>
              )}
            </TabsContent>
          </Tabs>
        </div>
      </DialogContent>
    </Dialog>
  )
}
