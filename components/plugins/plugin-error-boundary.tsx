"use client"

import { Component, type ErrorInfo, type ReactNode } from "react"
import { Button, Card, CardContent } from "@/components/ui"

interface Props {
  pluginId: string
  pluginName?: string
  onGoHome?: () => void
  children: ReactNode
}

interface State {
  hasError: boolean
  message: string
}

// Isolates rendering failures inside any plugin module so the Core Shell
// sidebar, header, terminal, and AI assistant remain fully operational.
export class PluginErrorBoundary extends Component<Props, State> {
  state: State = { hasError: false, message: "" }

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, message: error?.message || "插件视图渲染异常" }
  }

  componentDidCatch(_error: Error, _info: ErrorInfo) {
    // Contained within boundary; do not bubble crash to ConsoleShell.
  }

  componentDidUpdate(prevProps: Props) {
    if (prevProps.pluginId !== this.props.pluginId && this.state.hasError) {
      this.setState({ hasError: false, message: "" })
    }
  }

  render() {
    if (!this.state.hasError) return this.props.children
    const label = this.props.pluginName || this.props.pluginId
    return (
      <section role="alert" className="min-h-0 flex-1 overflow-auto p-6">
        <Card className="mx-auto max-w-xl">
          <CardContent className="space-y-4 p-6">
            <div className="space-y-1">
              <h2 className="text-base font-semibold text-foreground">插件「{label}」渲染失败</h2>
              <p className="text-sm text-muted-foreground">
                该异常已隔离在当前插件区域内，控制台导航、AI 助手与终端会话未受影响。
              </p>
              {this.state.message && (
                <p className="rounded-md border bg-muted/40 px-3 py-2 font-mono text-xs text-muted-foreground">
                  {this.state.message}
                </p>
              )}
            </div>
            <div className="flex flex-wrap gap-2">
              <Button size="sm" onClick={() => this.setState({ hasError: false, message: "" })}>
                重新加载插件
              </Button>
              {this.props.onGoHome && (
                <Button size="sm" variant="default" onClick={this.props.onGoHome}>
                  返回首页
                </Button>
              )}
            </div>
          </CardContent>
        </Card>
      </section>
    )
  }
}
