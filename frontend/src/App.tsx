import { useEffect, useRef, useState, type ReactNode } from 'react'
import { getTextWidth } from '@evenrealities/pretext'
/*
 * Components come in through their own subpaths rather than the
 * `even-toolkit/web` barrel. The barrel re-exports all 55 components, and two of
 * them drag in heavy or missing optional peers: DrawerShell imports react-router
 * (an optional peer this app does not install) and the chart components import
 * recharts. A barrel import therefore fails to bundle at all.
 *
 * AppShell has no subpath — the package does not export one — so the shell below
 * is written out. It is six lines of flex, and it drops the mobile-only fade
 * gradient AppShell paints over its scroll area.
 */
import { Badge } from 'even-toolkit/web/badge'
import { Button } from 'even-toolkit/web/button'
import { Card } from 'even-toolkit/web/card'
import { Divider } from 'even-toolkit/web/divider'
import { Input } from 'even-toolkit/web/input'
import { Loading } from 'even-toolkit/web/loading'
import { NavHeader } from 'even-toolkit/web/nav-header'
import { SegmentedControl } from 'even-toolkit/web/segmented-control'
import { StatusDot } from 'even-toolkit/web/status-dot'
import { Textarea } from 'even-toolkit/web/textarea'
import { Toggle } from 'even-toolkit/web/toggle'
import {
  IcEditDisplayAdj,
  IcFeatScreenOff,
  IcFeatTheme,
  IcGuideChevronSmallDrillDown,
  IcStatusBluetooth,
  IcStatusBluetoothDisconnected,
  IcStatusGlasses,
} from 'even-toolkit/web/icons/svg-icons'
import { EvenService } from '../bindings/even-glasses'
import type { CodexStatus, DashboardScheduleItem, DeviceCandidate, DeviceStatus } from '../bindings/even-glasses'

/*
 * The desktop client is a control surface for one lens page, so the window is
 * read top to bottom rather than as a phone-shaped stack: a lens preview that
 * folds away when nobody is watching it, a section switcher that stays put
 * while the panels scroll, then the panels of the active section in a grid.
 * The components are the library's; the arrangement is ours.
 */

const initialStatus: DeviceStatus = {
  connected: false,
  connecting: true,
  leftState: 'Scanning',
  rightState: 'Scanning',
  lastError: '',
  lastErrorAt: '',
  reconnectCount: 0,
  displayDurationSeconds: 5,
  deviceId: '',
  deviceName: '',
  audioAvailable: false,
  settingsKnown: false,
  capabilities: {
    text: false, nativeText: false, nativeList: false, image: false,
    microphoneLC3: false, inputEvents: false, deviceSettings: false,
    brightness: false, headUp: false, screenPosition: false, dashboard: false,
  },
  settings: {
    batteryPercent: 0, charging: false, leftFirmwareVersion: '', rightFirmwareVersion: '',
    brightness: 0, autoBrightness: false, headUpEnabled: false, headUpAngle: 0,
    wearDetection: false, silentMode: false, screenDepth: 0, screenHeight: 0,
  },
  audioActive: false,
  audioFrames: 0,
  audioBytes: 0,
  lastEvent: { kind: '', name: '', itemName: '', type: '', itemIndex: 0 },
}

// Kept in step with stillStates in statusicon.go, including its shape. That one
// is an exception list on purpose: a state the Go side does not recognise counts
// as work in progress, so the lens fails towards showing motion rather than an
// empty icon. Listing the working states positively here would drift the moment
// an adapter invented a new one, and the preview would sit still while the lens
// was sweeping.
const stillStates = new Set(['needs_input', 'permission', 'paused', 'done', 'idle', 'error', 'failed'])

const capabilityLabels: Record<keyof DeviceStatus['capabilities'], string> = {
  text: '文本',
  nativeText: '原生文本',
  nativeList: '原生列表',
  image: '图片',
  microphoneLC3: '麦克风',
  inputEvents: '输入事件',
  deviceSettings: '设备信息',
  brightness: '亮度',
  headUp: '抬头显示',
  screenPosition: '屏幕位置',
  dashboard: 'Dashboard',
}

// Display names only — the states themselves come from the hook adapters in
// scripts/. An unknown state falls through to its raw value, so a new adapter
// shows up as itself instead of as an empty label.
const stateLabels: Record<string, string> = {
  idle: '空闲',
  thinking: '思考中',
  tool: '调用工具',
  compacting: '整理上下文',
  needs_input: '等待输入',
  permission: '等待授权',
  paused: '已暂停',
  done: '已完成',
  error: '出错',
  failed: '失败',
}

// An empty state means no session has been pushed yet, which is not the same as
// an unrecognised one — nothing is running, so nothing should sweep.
const isWorkingState = (state: string) => state !== '' && !stillStates.has(state)

type Notice = { text: string; error: boolean }

type SectionKey = 'overview' | 'display' | 'settings' | 'labs'

const sections: { value: SectionKey; label: string; hint: string }[] = [
  { value: 'overview', label: '概览', hint: '连接、状态桥与实时输入' },
  { value: 'display', label: '显示', hint: '把文本、列表或图片推到镜片' },
  { value: 'settings', label: '设备设置', hint: '亮度、抬头显示与屏幕位置' },
  { value: 'labs', label: '实验与诊断', hint: 'Dashboard 与连接排查' },
]

type PreviewPage = {
  list?: { items: string[]; itemWidth: number; selectBorder: boolean }
  text: string
  style: {
    X: number; Y: number; Width: number; Height: number
    BorderWidth: number; BorderColor: number; BorderRadius: number; PaddingLength: number
  }
  icon: { X: number; Y: number; Width: number; Height: number; BMP: string } | null
}

const previewColour = (level: number) => `rgba(155, 255, 159, ${Math.max(0, Math.min(15, level)) / 15})`

function drawLensText(context: CanvasRenderingContext2D, text: string, x: number, y: number) {
  let cursor = x
  for (const character of text) {
    context.fillText(character, cursor, y)
    cursor += getTextWidth(character)
  }
}

async function drawPreview(canvas: HTMLCanvasElement, page: PreviewPage) {
  const context = canvas.getContext('2d')
  if (!context) return
  context.clearRect(0, 0, canvas.width, canvas.height)
  context.fillStyle = '#000'
  context.fillRect(0, 0, canvas.width, canvas.height)

  const style = page.style
  if (style.BorderWidth > 0) {
    context.strokeStyle = previewColour(style.BorderColor)
    context.lineWidth = style.BorderWidth
    context.beginPath()
    context.roundRect(
      style.X + style.BorderWidth / 2,
      style.Y + style.BorderWidth / 2,
      style.Width - style.BorderWidth,
      style.Height - style.BorderWidth,
      style.BorderRadius,
    )
    context.stroke()
  }
  if (page.list) {
	const inset = style.BorderWidth + style.PaddingLength
	const itemX = style.X + inset
	const itemY = style.Y + inset
    context.save()
    context.beginPath()
    context.rect(style.X, style.Y, style.Width, style.Height)
    context.clip()
    context.font = '20px "PingFang SC", "SF Pro Text", sans-serif'
    context.textBaseline = 'middle'
    // Firmware owns row layout and scrolling; Canvas approximates the initial list.
    page.list.items.forEach((label, index) => {
      const y = itemY + index * 40
      if (index === 0 && page.list!.selectBorder) {
        context.strokeStyle = previewColour(15)
        context.lineWidth = 1
		context.beginPath()
		context.roundRect(itemX + 1, y + 1, page.list!.itemWidth - 2, 38, 6)
		context.stroke()
      }
      context.fillStyle = previewColour(15)
		drawLensText(context, label, itemX + 12, y + 20)
    })
    context.restore()
    return
  }

  const inset = style.BorderWidth + style.PaddingLength
  context.save()
  context.beginPath()
  context.rect(style.X + inset, style.Y + inset, style.Width - inset * 2, style.Height - inset * 2)
  context.clip()
  context.fillStyle = previewColour(15)
  context.font = '20px "PingFang SC", "SF Pro Text", sans-serif'
  context.textBaseline = 'top'
  page.text.split('\n').forEach((line, index) => {
	drawLensText(context, line, style.X + inset, style.Y + inset + index * 27)
  })
  context.restore()

  if (page.icon?.BMP) {
    const image = new Image()
    await new Promise<void>((resolve, reject) => {
      image.onload = () => resolve()
      image.onerror = () => reject(new Error('无法解码状态图标'))
      image.src = `data:image/bmp;base64,${page.icon!.BMP}`
    })
    context.drawImage(image, page.icon.X, page.icon.Y, page.icon.Width, page.icon.Height)
  }
}

// The canvas renders whatever the service last handed to the glasses, including
// the BMP status icon, so the panel is a faithful readout and not a mock-up.
// It is only mounted while the preview is unfolded.
function LensCanvas() {
  const canvas = useRef<HTMLCanvasElement>(null)
  const [error, setError] = useState('')
  const [nativeError, setNativeError] = useState('')
  useEffect(() => {
    let stopped = false
    let timer: ReturnType<typeof setTimeout>
    async function refresh() {
      try {
        const page = JSON.parse(await EvenService.PreviewPage()) as PreviewPage
        if (stopped) return
        if (canvas.current) await drawPreview(canvas.current, page)
        setError('')
      } catch (error) {
        if (stopped) return
        setError(String(error))
      }
      if (!stopped) timer = setTimeout(refresh, 250)
    }
    void refresh()
    return () => { stopped = true; clearTimeout(timer) }
  }, [])
  const openNativeSimulator = async () => {
    setNativeError('')
    try {
      await EvenService.StartNativeSimulator()
    } catch (error) {
      setNativeError(String(error))
    }
  }
  return (
    <div className="lens-simulator-shell">
      <div className="lens-simulator-toolbar">
        <span><i /> 内置 G2 PREVIEW</span>
        <span className="lens-simulator-actions">
          <span>576 × 288 · 4-bit</span>
          <button type="button" onClick={() => void openNativeSimulator()}>打开原生模拟器</button>
        </span>
      </div>
      <div className="lens-display">
        <canvas ref={canvas} width={576} height={288} aria-label="内置眼镜画面预览" />
        {(error || nativeError) && <div className="lens-empty">{error || nativeError}</div>}
      </div>
    </div>
  )
}

/*
 * The lens preview is folded by default: it is a readout, not the thing you came
 * to the window for, and at 576×288 it costs a third of the viewport. The folded
 * strip still carries the state and the row count, so hiding it never hides the
 * answer to "is something running".
 */
function LensPanel({ open, onToggle, summary, state, sweeping, rows }: {
  open: boolean
  onToggle: () => void
  summary: string
  state: string
  sweeping: boolean
  rows: string[]
}) {
  return (
    <Card padding="none" className="overflow-hidden border border-border">
      <button
        type="button"
        className="lens-fold-head"
        aria-expanded={open}
        onClick={onToggle}
      >
        <span className="lens-mark" aria-hidden="true">
          <IcStatusGlasses width={18} height={18} />
        </span>
        <span className="min-w-0 flex-1">
          <span className="flex items-center gap-2">
            <span className="text-normal-title text-text">内置眼镜预览</span>
            {sweeping ? <Loading size={14} /> : null}
          </span>
          <span className="mt-0.5 block truncate text-detail text-text-dim">{summary}</span>
        </span>
        {state ? <Badge variant="neutral">{state}</Badge> : null}
        <IcGuideChevronSmallDrillDown
          className={`lens-fold-chevron${open ? ' is-open' : ''}`}
          width={16}
          height={16}
        />
      </button>
      {open ? <LensPanelBody rows={rows} /> : null}
    </Card>
  )
}

// Rows come straight from CodexStatus, which is the composed list the lens is
// showing. Printing them beside the canvas makes the page reviewable without
// putting the glasses on, and fills the width the 576px canvas cannot use.
function LensPanelBody({ rows }: { rows: string[] }) {
  return (
    <div className="lens-fold-body">
      <LensCanvas />
      <div className="min-w-0">
        <div className="mb-2 flex items-baseline justify-between gap-3">
          <span className="text-subtitle text-text-dim">发送中的镜片行</span>
          <span className="text-detail text-text-muted">{rows.length} 行 · 直接取状态桥</span>
        </div>
        {rows.length ? (
          <ol className="lens-rows">
            {rows.map((row, index) => (
              <li key={index} className="lens-row">
                <span className="lens-row-index">{index + 1}</span>
                <span className="lens-row-text">{row}</span>
              </li>
            ))}
          </ol>
        ) : (
          <p className="lens-rows lens-rows-empty">当前没有推送任何行</p>
        )}
        <p className="mt-3 text-detail leading-relaxed text-text-muted">
          直接渲染发送给眼镜的文本、边框与 BMP 图标；无需启动外部模拟器，最终显示以真机为准。
        </p>
      </div>
    </div>
  )
}

/*
 * SectionHeader is sized for a phone screen — one 20px title per screen. A
 * desktop panel grid stacks a dozen of them, so panels use the system's
 * normal-title with a detail line underneath, on the same tracking and colour
 * tokens. The border is what makes a white card read as a card on the grey page.
 */
function Panel({ title, hint, action, className, children }: {
  title: string
  hint?: ReactNode
  action?: ReactNode
  className?: string
  children: ReactNode
}) {
  return (
    <Card className={`border border-border ${className ?? ''}`}>
      <div className="mb-3 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-normal-title text-text">{title}</h2>
          {hint ? <p className="mt-1 text-detail leading-[1.55] text-text-dim">{hint}</p> : null}
        </div>
        {action ? <div className="shrink-0">{action}</div> : null}
      </div>
      {children}
    </Card>
  )
}

// Tiles carry the read-only values: arms, battery, counters. They sit on
// surface-light so a row of them reads as one grouped block.
function Tile({ label, value, hint }: { label: string; value: ReactNode; hint?: ReactNode }) {
  return (
    <div className="min-w-0 rounded-[6px] bg-surface-light px-3 py-2">
      <div className="text-detail text-text-muted">{label}</div>
      <div className="mt-0.5 truncate text-subtitle text-text">{value}</div>
      {hint ? <div className="mt-0.5 truncate text-detail text-text-muted">{hint}</div> : null}
    </div>
  )
}

// The toolkit forces `appearance: none` on every select and clears its
// background-image, so the native arrow cannot be styled back in — the chevron
// has to be a sibling element. Without it the two device pickers look like
// plain text boxes.
function DeviceSelect({ label, value, onChange, children }: {
  label: string
  value: string
  onChange: (value: string) => void
  children: ReactNode
}) {
  return (
    <div className="relative">
      <select
        aria-label={label}
        className="h-9 w-full appearance-none rounded-[6px] bg-input-bg pl-3 pr-8 text-subtitle text-text outline-none"
        value={value}
        onChange={event => onChange(event.target.value)}
      >
        {children}
      </select>
      <IcGuideChevronSmallDrillDown
        className="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 text-text-dim"
        width={16}
        height={16}
      />
    </div>
  )
}

function SwitchRow({ label, checked, onChange, disabled }: {
  label: string
  checked: boolean
  onChange: (value: boolean) => void
  disabled?: boolean
}) {
  return (
    <div className="flex items-center justify-between gap-3 py-1">
      <span className="text-subtitle text-text">{label}</span>
      <Toggle checked={checked} onChange={onChange} disabled={disabled} />
    </div>
  )
}

function App() {
  const [section, setSection] = useState<SectionKey>('overview')
  const [previewOpen, setPreviewOpen] = useState(false)
  const [status, setStatus] = useState<DeviceStatus>(initialStatus)
  const [codex, setCodex] = useState<CodexStatus>({ available: false, message: '正在连接状态桥', state: '', rows: [] })
  const [text, setText] = useState('Hello from Even Glasses')
  const [list, setList] = useState('Today\nFocus mode\nTake a break')
  const [imageData, setImageData] = useState('')
  const [imageName, setImageName] = useState('')
  const [mode, setMode] = useState<'native' | 'teleprompter'>('native')
  const [displaySeconds, setDisplaySeconds] = useState(5)
  const [busy, setBusy] = useState(false)
  const [dark, setDark] = useState(false)
  const [notice, setNotice] = useState<Notice>({ text: '准备连接你的 Even G2', error: false })
  const [candidates, setCandidates] = useState<DeviceCandidate[]>([])
  const [selectedLeft, setSelectedLeft] = useState('')
  const [selectedRight, setSelectedRight] = useState('')
  const [brightness, setBrightness] = useState(50)
  const [autoBrightness, setAutoBrightness] = useState(false)
  const [headUpEnabled, setHeadUpEnabled] = useState(false)
  const [headUpAngle, setHeadUpAngle] = useState(30)
  const [screenHeight, setScreenHeight] = useState(4)
  const [screenDepth, setScreenDepth] = useState(1)
  const [dashboardExperimental, setDashboardExperimental] = useState(false)
  const [dashboardHalfDay, setDashboardHalfDay] = useState(false)
  const [dashboardCelsius, setDashboardCelsius] = useState(true)
  const [scheduleJSON, setScheduleJSON] = useState('[{"id":1,"title":"示例日程","location":"办公室","time":"10:00","endTimestamp":0}]')

  // The theme attribute has to sit on <html>: the token aliases in theme.css are
  // declared on :root, so a value set further down would not reach them.
  useEffect(() => {
    document.documentElement.dataset.theme = dark ? 'dark' : 'light'
  }, [dark])

  const refresh = async () => {
    try {
      const [device, bridge] = await Promise.all([EvenService.Status(), EvenService.CodexStatus()])
      setStatus(device)
      setCodex(bridge)
    } catch (error) {
      console.error(error)
    }
  }

  useEffect(() => {
    void refresh()
    const timer = setInterval(() => void refresh(), 500)
    return () => clearInterval(timer)
  }, [])

  useEffect(() => {
    if (!status.settingsKnown) return
    setBrightness(status.settings.brightness)
    setAutoBrightness(status.settings.autoBrightness)
    setHeadUpEnabled(status.settings.headUpEnabled)
    setHeadUpAngle(status.settings.headUpAngle)
    setScreenHeight(status.settings.screenHeight)
    setScreenDepth(status.settings.screenDepth)
  }, [
    status.settingsKnown,
    status.settings.brightness,
    status.settings.autoBrightness,
    status.settings.headUpEnabled,
    status.settings.headUpAngle,
    status.settings.screenHeight,
    status.settings.screenDepth,
  ])

  const run = async (task: () => Promise<unknown>, success: string) => {
    setBusy(true)
    try {
      await task()
      setNotice({ text: success, error: false })
      await refresh()
    } catch (error) {
      setNotice({ text: error instanceof Error ? error.message : String(error), error: true })
      await refresh()
    } finally {
      setBusy(false)
    }
  }

  const connect = () => run(async () => setStatus(await EvenService.Connect()), '已开始主动搜索 Even G2')
  const disconnect = () => run(EvenService.Disconnect, '已断开连接')
  const scanCandidates = () =>
    run(async () => {
      const found = (await EvenService.ScanCandidates()) ?? []
      setCandidates(found)
      const left = found.find(candidate => candidate.arm === 'LEFT')?.address ?? ''
      const right = found.find(candidate => candidate.arm === 'RIGHT')?.address ?? ''
      setSelectedLeft(left)
      setSelectedRight(right)
      if (!left || !right) throw new Error('没有扫描到完整的左右镜腿，请确认眼镜已开机并靠近电脑')
    }, '扫描完成，请确认左右镜腿后连接')
  const connectSelected = () =>
    run(async () => setStatus(await EvenService.ConnectDevice(selectedLeft, selectedRight)), '已连接所选 Even G2')
  const clearDisplay = () => run(EvenService.ClearDisplay, '眼镜显示已清空')
  const saveBrightness = () => run(async () => { await EvenService.SetBrightness(brightness, autoBrightness); await EvenService.RequestDeviceSettings() }, '亮度设置已发送')
  const saveHeadUp = () => run(async () => { await EvenService.SetHeadUp(headUpEnabled, headUpAngle); await EvenService.RequestDeviceSettings() }, '抬头显示设置已发送')
  const saveScreenPosition = () => run(async () => { await EvenService.SetScreenPosition(screenHeight, screenDepth); await EvenService.RequestDeviceSettings() }, '屏幕位置设置已发送')
  const startMicrophone = () => run(EvenService.StartMicrophone, '眼镜麦克风已启动')
  const stopMicrophone = () => run(EvenService.StopMicrophone, '眼镜麦克风已停止')
  const showDashboard = () => run(EvenService.ShowDashboard, '已恢复原生 Dashboard')
  const configureDashboard = () => run(
    () => EvenService.ConfigureDashboard([1, 2, 3, 4, 5], dashboardHalfDay, dashboardCelsius, dashboardExperimental),
    'Dashboard 配置已发送',
  )
  const pushSchedule = () => run(async () => {
    const items = JSON.parse(scheduleJSON) as DashboardScheduleItem[]
    await EvenService.PushDashboardSchedule(items, dashboardExperimental)
  }, 'Dashboard 日程已发送')
  const clearSchedule = () => run(() => EvenService.ClearDashboardSchedule(dashboardExperimental), 'Dashboard 日程已清空')
  const saveDisplayDuration = () => run(() => EvenService.SetDisplayDuration(displaySeconds), `显示时长已设为 ${displaySeconds} 秒`)
  const sendText = () =>
    run(
      () => (mode === 'teleprompter' ? EvenService.ShowTeleprompter(text) : EvenService.ShowNativeText(text)),
      mode === 'teleprompter' ? '提词器内容已发送' : '全镜片原生文本已发送',
    )
  const sendList = () => run(() => EvenService.ShowList(list.split('\n')), '可点击列表已发送到眼镜')
	const chooseImage = (file?: File) => {
		if (!file) return
		setImageName(file.name)
		const reader = new FileReader()
		reader.onload = () => setImageData(typeof reader.result === 'string' ? reader.result : '')
		reader.readAsDataURL(file)
	}
	const sendImage = () => run(() => EvenService.ShowImage(imageData), '图片已转换并发送到眼镜')
  const calibrate = () =>
    run(async () => {
      const result = await EvenService.LensCalibration()
      if (result?.length) setCodex(previous => ({ ...previous, rows: result, message: '镜片校准列表已推送（请在眼镜上查看读数）' }))
    }, '镜片校准列表已发送，请对照眼镜读数并反馈行宽 / 行数 / 符号')

  const rows = codex.rows ?? []
  const sweeping = isWorkingState(codex.state)
  const connectionLabel = status.connected ? '已连接' : status.connecting ? '正在连接' : '未连接'
  const stateText = codex.state ? (stateLabels[codex.state] ?? codex.state) : '空闲'
  const activeSection = sections.find(item => item.value === section) ?? sections[0]
  const enabledCapabilities = (Object.entries(status.capabilities) as [keyof DeviceStatus['capabilities'], boolean][])
    .filter(([, enabled]) => enabled)

  // One line has to answer "is anything running, and what does the lens say".
  const previewSummary = !status.connected
    ? '眼镜未连接 · 等待任务页面发送'
    : !rows.length
      ? '当前没有活动页面'
      : sweeping
        ? `${stateText} · 状态图标正在更新 · ${rows.length} 行`
        : `${stateText} · ${rows.length} 行`

  return (
    <div className="flex h-dvh flex-col overflow-hidden bg-bg">
      <div className="window-drag shrink-0">
        <NavHeader
          title={
            <div className="flex items-center justify-center gap-2">
              <StatusDot connected={status.connected} />
              <span className="text-normal-body text-text-dim">{connectionLabel}</span>
            </div>
          }
          left={
            <div className="titlebar-inset flex items-center gap-2">
              <IcStatusGlasses width={22} height={22} />
              <span className="text-medium-title">Even Glasses</span>
            </div>
          }
          right={
            <Button
              variant="ghost"
              size="icon"
              aria-label={dark ? '切换到亮色主题' : '切换到暗色主题'}
              onClick={() => setDark(value => !value)}
            >
              <IcFeatTheme width={20} height={20} />
            </Button>
          }
        />
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-[1180px] px-5 pt-5">
          <LensPanel
            open={previewOpen}
            onToggle={() => setPreviewOpen(value => !value)}
            summary={previewSummary}
            state={codex.state ? stateText : ''}
            sweeping={status.connected && sweeping}
            rows={rows}
          />
        </div>

        <div className="sticky top-0 z-20 mt-5 border-b border-border bg-bg/90 backdrop-blur-sm">
          <div className="mx-auto flex w-full max-w-[1180px] items-center gap-4 px-5 py-2.5">
            <SegmentedControl
              className="w-full max-w-[520px]"
              options={sections.map(item => ({ value: item.value, label: item.label }))}
              value={section}
              onValueChange={value => setSection(value as SectionKey)}
            />
            <span className="ml-auto hidden shrink-0 text-detail text-text-muted lg:block">{activeSection.hint}</span>
          </div>
        </div>

        <div className="mx-auto w-full max-w-[1180px] px-5 pb-10 pt-5">
          {section === 'overview' ? (
            <div className="grid items-start gap-4 lg:grid-cols-[minmax(0,1.05fr)_minmax(0,1fr)]">
              <Panel
                title="设备连接"
                hint="蓝牙连接左右镜腿；连接成功后自动同步设备参数"
              >
                <div className="flex items-center justify-between gap-3 rounded-[6px] bg-surface-light px-3 py-2.5">
                  <div className="flex min-w-0 items-center gap-2.5">
                    <StatusDot connected={status.connected} />
                    <span className="text-normal-title">Even G2</span>
                  </div>
                  <Badge variant={status.connected ? 'positive' : status.connecting ? 'neutral' : 'negative'}>{connectionLabel}</Badge>
                </div>
                <div className="mt-3 grid grid-cols-2 gap-2">
                  <Tile label="左臂" value={status.leftState} />
                  <Tile label="右臂" value={status.rightState} />
                </div>
                {status.lastError ? (
                  <div className="mt-3 rounded-[6px] bg-negative-alpha px-3 py-2 text-subtitle text-negative">
                    <p className="break-words">{status.lastError}</p>
                    <p className="mt-1 text-detail opacity-70">{status.lastErrorAt || '刚刚'} · 自动重连 {status.reconnectCount} 次</p>
                  </div>
                ) : null}
                <div className="mt-4 flex gap-2">
                  <Button className="flex-1" disabled={busy || status.connected || status.connecting} onClick={connect}>
                    <IcStatusBluetooth width={20} height={20} />
                    {status.connecting ? '正在搜索' : '连接'}
                  </Button>
                  {(status.connected || status.connecting) && (
                    <Button variant="secondary" disabled={busy} onClick={disconnect}>
                      <IcStatusBluetoothDisconnected width={20} height={20} />
                      断开
                    </Button>
                  )}
                </div>
                {!status.connected ? (
                  <div className="mt-3 flex flex-col gap-2">
                    <Button variant="secondary" disabled={busy} onClick={scanCandidates}>扫描并选择设备</Button>
                    {candidates.length > 0 ? (
                      <div className="flex flex-col gap-2 rounded-[6px] bg-surface-light p-3">
                        <DeviceSelect label="左镜腿" value={selectedLeft} onChange={setSelectedLeft}>
                          <option value="">选择左镜腿</option>
                          {candidates.filter(candidate => candidate.arm === 'LEFT').map(candidate => (
                            <option key={candidate.address} value={candidate.address}>{candidate.name} · {candidate.address} · {candidate.rssi} dBm</option>
                          ))}
                        </DeviceSelect>
                        <DeviceSelect label="右镜腿" value={selectedRight} onChange={setSelectedRight}>
                          <option value="">选择右镜腿</option>
                          {candidates.filter(candidate => candidate.arm === 'RIGHT').map(candidate => (
                            <option key={candidate.address} value={candidate.address}>{candidate.name} · {candidate.address} · {candidate.rssi} dBm</option>
                          ))}
                        </DeviceSelect>
                        <Button disabled={busy || !selectedLeft || !selectedRight} onClick={connectSelected}>连接所选镜腿</Button>
                      </div>
                    ) : null}
                  </div>
                ) : null}
                {status.connected ? (
                  <p className="mt-3 break-all text-detail text-text-muted">
                    {status.deviceName || 'Even G2'} · {status.deviceId} · 麦克风{status.audioAvailable ? '可用' : '不可用'}
                  </p>
                ) : null}
                {status.connected && enabledCapabilities.length ? (
                  <div className="mt-3 flex flex-wrap gap-1">
                    {enabledCapabilities.map(([name]) => <Badge key={name}>{capabilityLabels[name]}</Badge>)}
                  </div>
                ) : null}
              </Panel>

              <div className="flex flex-col gap-4">
                <Panel
                  title="状态桥"
                  hint="agent hook 落盘后由本地进程轮询推送到镜片"
                  action={<Badge variant={codex.available ? 'positive' : 'negative'}>{codex.available ? '已接入' : '未接入'}</Badge>}
                >
                  <p className="text-subtitle leading-relaxed text-text-dim">{codex.message}</p>
                  <div className="mt-3 grid grid-cols-2 gap-2">
                    <Tile label="当前状态" value={status.connected ? stateText : '—'} />
                    <Tile label="镜片行" value={`${rows.length} 行`} hint={sweeping ? '状态图标正在更新' : undefined} />
                  </div>
                </Panel>

                <Panel title="输入与麦克风" hint="眼镜端只有列表点按、上下滑和双击，没有长按或多键">
                  <div className="flex gap-2">
                    <Button className="flex-1" disabled={busy || !status.connected || !status.audioAvailable || status.audioActive} onClick={startMicrophone}>开始录音</Button>
                    <Button variant="secondary" className="flex-1" disabled={busy || !status.audioActive} onClick={stopMicrophone}>停止录音</Button>
                  </div>
                  <div className="mt-3 grid grid-cols-2 gap-2">
                    <Tile label="LC3 帧" value={status.audioFrames} />
                    <Tile label="音频数据" value={`${(status.audioBytes / 1024).toFixed(1)} KiB`} />
                  </div>
                  <Divider className="my-3" />
                  {status.lastEvent.kind ? (
                    <div className="text-subtitle leading-relaxed text-text-dim">
                      <div>{status.lastEvent.kind} · {status.lastEvent.type}</div>
                      <div>{status.lastEvent.name || '系统事件'}{status.lastEvent.itemName ? ` · ${status.lastEvent.itemName} #${status.lastEvent.itemIndex}` : ''}</div>
                    </div>
                  ) : <p className="text-subtitle text-text-muted">尚未收到眼镜输入事件</p>}
                </Panel>
              </div>
            </div>
          ) : null}

          {section === 'display' ? (
            <div className="grid items-start gap-4 lg:grid-cols-2">
              <div className="flex flex-col gap-4">
                <Panel
                  title="发送文本"
                  hint="原生文本填满整块镜片；提词器走 25 列 × 10 行的长文分页通道，18 秒后回落"
                >
                  <SegmentedControl
                    size="small"
                    className="mb-3 w-full"
                    options={[
                      { value: 'native', label: '原生文本' },
                      { value: 'teleprompter', label: '提词器' },
                    ]}
                    value={mode}
                    onValueChange={value => setMode(value as 'native' | 'teleprompter')}
                  />
                  <Textarea rows={7} value={text} onChange={event => setText(event.target.value)} placeholder="输入要显示的内容" />
                  <Button className="mt-3 w-full" disabled={busy || !status.connected || !text.trim()} onClick={sendText}>
                    发送文本
                  </Button>
                </Panel>

                <Panel title="页面时长与快捷操作" hint="agent 工作时不计时，状态停下来才开始倒数">
                  <label className="mb-2 block text-detail text-text-dim" htmlFor="display-seconds">
                    内容在眼镜上保留多久
                  </label>
                  <div className="flex items-center gap-2">
                    <Input
                      id="display-seconds"
                      type="number"
                      min={1}
                      max={300}
                      className="h-9 w-24 text-subtitle"
                      value={displaySeconds}
                      onChange={event => setDisplaySeconds(Number(event.target.value))}
                    />
                    <span className="text-subtitle text-text-dim">秒</span>
                    <Button
                      variant="secondary"
                      size="sm"
                      className="ml-auto"
                      disabled={busy || displaySeconds < 1 || displaySeconds > 300}
                      onClick={saveDisplayDuration}
                    >
                      应用
                    </Button>
                  </div>
                  <div className="mt-3 flex flex-col gap-2">
                    <Button variant="secondary" disabled={busy || !status.connected} onClick={clearDisplay}>
                      <IcFeatScreenOff width={20} height={20} />
                      清空眼镜显示
                    </Button>
                    <Button variant="secondary" disabled={busy || !status.connected} onClick={calibrate}>
                      <IcEditDisplayAdj width={20} height={20} />
                      镜片校准
                    </Button>
                  </div>
                </Panel>
              </div>

              <div className="flex flex-col gap-4">
                <Panel title="原生列表" hint="每行一个项目，可在眼镜端按选中那行">
                  <Textarea rows={5} value={list} onChange={event => setList(event.target.value)} placeholder="每行一个项目" />
                  <Button variant="secondary" className="mt-3 w-full" disabled={busy || !status.connected} onClick={sendList}>
                    发送列表
                  </Button>
                </Panel>

                <Panel title="图片" hint="PNG / JPEG 自动适配 576×288，并转换为眼镜的 4-bit 灰度图">
                  <label className="flex min-h-[168px] cursor-pointer items-center justify-center rounded-[6px] border border-dashed border-border bg-input-bg px-4 text-center text-subtitle text-text-dim transition-colors hover:bg-surface-light">
                    <input className="hidden" type="file" accept="image/png,image/jpeg" onChange={event => chooseImage(event.target.files?.[0])} />
                    {imageName || '选择 PNG 或 JPEG 图片'}
                  </label>
                  <Button className="mt-3 w-full" disabled={busy || !status.connected || !imageData} onClick={sendImage}>发送图片</Button>
                </Panel>
              </div>
            </div>
          ) : null}

          {section === 'settings' ? (
            <div className="grid items-start gap-4 lg:grid-cols-2">
              <div className="flex flex-col gap-4">
              <Panel title="亮度" hint="关闭自动亮度后才能手动拖动；应用后重新读取设备参数">
                <div className="display-adjustment-panel">
                  <div className="display-adjustment-stage" aria-hidden="true">
                    <span className="display-volume back" />
                    <span className="display-volume front" />
                    <span className="display-plane" />
                  </div>
                  <label className="vertical-control">
                    <span className="vertical-control-icon">☀</span>
                    <input
                      type="range"
                      min={0}
                      max={100}
                      value={brightness}
                      disabled={autoBrightness}
                      aria-label="亮度"
                      onChange={event => setBrightness(Number(event.target.value))}
                    />
                    <strong>{autoBrightness ? '自动' : `${brightness}%`}</strong>
                  </label>
                </div>
                <div className="mt-3">
                  <SwitchRow label="自动亮度" checked={autoBrightness} onChange={setAutoBrightness} />
                </div>
                <Button variant="secondary" size="sm" className="mt-2 w-full" disabled={busy || brightness < 0 || brightness > 100} onClick={saveBrightness}>应用亮度</Button>
              </Panel>

              <Panel title="抬头显示" hint="抬起头部后把当前页面重新推到眼前">
                <SwitchRow label="启用抬头显示" checked={headUpEnabled} onChange={setHeadUpEnabled} />
                <div className="mt-3 flex items-center justify-between gap-3">
                  <span className="text-subtitle text-text-dim">抬头角度</span>
                  <div className="flex items-center gap-2">
                    <Input
                      type="number"
                      min={0}
                      max={60}
                      className="h-9 w-24 text-subtitle"
                      value={headUpAngle}
                      onChange={event => setHeadUpAngle(Number(event.target.value))}
                    />
                    <span className="text-subtitle text-text-dim">度</span>
                  </div>
                </div>
                <Button variant="secondary" size="sm" className="mt-3 w-full" disabled={busy || headUpAngle < 0 || headUpAngle > 60} onClick={saveHeadUp}>应用抬头设置</Button>
              </Panel>
              </div>

              <div className="flex flex-col gap-4">
              <Panel title="屏幕位置" hint="视场高度与距离共同决定投放位置，改完以真机观感为准">
                <div className="screen-position-controls">
                  <label className="vertical-control screen-height-control">
                    <span className="vertical-control-icon">↕</span>
                    <input type="range" min={0} max={12} value={screenHeight} aria-label="视场高度" onChange={event => setScreenHeight(Number(event.target.value))} />
                    <strong>{screenHeight}</strong>
                  </label>
                  <div className="min-w-0 flex-1">
                    <span className="mb-2 block text-detail text-text-muted">视场距离</span>
                    <SegmentedControl
                      size="small"
                      className="w-full"
                      options={[
                        { value: '0', label: '稍近' },
                        { value: '1', label: '标准' },
                        { value: '2', label: '稍远' },
                      ]}
                      value={String(screenDepth)}
                      onValueChange={value => setScreenDepth(Number(value))}
                    />
                    <p className="mt-3 text-detail leading-relaxed text-text-muted">只改这里不会立刻生效，需要点下面的应用按钮。</p>
                  </div>
                </div>
                <Button variant="secondary" size="sm" className="mt-3 w-full" disabled={busy || screenHeight < 0 || screenHeight > 12 || screenDepth < 0 || screenDepth > 2} onClick={saveScreenPosition}>应用屏幕位置</Button>
              </Panel>

              <Panel title="设备参数" hint="连接眼镜后自动同步，这里是最近一次读到的值">
                {status.settingsKnown ? (
                  <>
                    <div className="grid grid-cols-2 gap-2">
                      <Tile label="电量" value={`${status.settings.batteryPercent}%`} hint={status.settings.charging ? '充电中' : undefined} />
                      <Tile label="佩戴检测" value={status.settings.wearDetection ? '已佩戴' : '未佩戴'} />
                      <Tile label="静音模式" value={status.settings.silentMode ? '开启' : '关闭'} />
                      <Tile label="固件" value={`L ${status.settings.leftFirmwareVersion || '未知'}`} hint={`R ${status.settings.rightFirmwareVersion || '未知'}`} />
                    </div>
                    <Divider className="my-3" />
                    <div className="flex flex-col gap-2 text-subtitle text-text-dim">
                      <div className="flex justify-between"><span>设备名</span><span className="text-text">{status.deviceName || 'Even G2'}</span></div>
                      <div className="flex justify-between gap-4"><span className="shrink-0">设备 ID</span><span className="break-all text-right text-text">{status.deviceId || '—'}</span></div>
                    </div>
                  </>
                ) : status.connected ? (
                  <p className="py-6 text-center text-subtitle text-text-dim">正在读取设备设置…</p>
                ) : (
                  <p className="py-6 text-center text-subtitle text-text-muted">连接眼镜后显示</p>
                )}
              </Panel>
              </div>
            </div>
          ) : null}

          {section === 'labs' ? (
            <div className="grid items-start gap-4 lg:grid-cols-2">
              <Panel title="Dashboard" hint="恢复眼镜自带的信息页；实验开关会影响组件排序与单位">
                <Button className="w-full" disabled={busy || !status.connected} onClick={showDashboard}>恢复原生 Dashboard</Button>
                <Divider className="my-3" />
                <SwitchRow label="启用实验功能" checked={dashboardExperimental} onChange={setDashboardExperimental} />
                {dashboardExperimental ? (
                  <div className="mt-3 flex flex-col gap-2">
                    <SwitchRow label="12 小时制" checked={dashboardHalfDay} onChange={setDashboardHalfDay} />
                    <SwitchRow label="摄氏温度" checked={dashboardCelsius} onChange={setDashboardCelsius} />
                    <Button variant="secondary" className="mt-1" disabled={busy || !status.connected} onClick={configureDashboard}>应用默认组件排序</Button>
                    <Textarea rows={4} value={scheduleJSON} onChange={event => setScheduleJSON(event.target.value)} />
                    <div className="flex gap-2">
                      <Button variant="secondary" className="flex-1" disabled={busy || !status.connected} onClick={pushSchedule}>写入日程</Button>
                      <Button variant="secondary" className="flex-1" disabled={busy || !status.connected} onClick={clearSchedule}>清空日程</Button>
                    </div>
                  </div>
                ) : null}
              </Panel>

              <Panel title="连接诊断" hint="自动重连与最近一次异常的原始信息">
                <div className="grid grid-cols-3 gap-2">
                  <Tile label="连接状态" value={connectionLabel} />
                  <Tile label="自动重连" value={`${status.reconnectCount} 次`} />
                  <Tile label="最近异常" value={status.lastErrorAt || '无'} hint={status.lastError || undefined} />
                </div>
                {status.lastError ? (
                  <p className="mt-3 break-words rounded-[6px] bg-negative-alpha px-3 py-2 text-detail text-negative">{status.lastError}</p>
                ) : null}
              </Panel>

              <Panel title="状态桥原始输出" hint="镜片行就是这里返回的内容，排查排版时可以直接对照" className="lg:col-span-2">
                <div className="flex items-center gap-3">
                  <Badge variant={codex.available ? 'positive' : 'negative'}>{codex.available ? '已接入' : '未接入'}</Badge>
                  <span className="text-detail text-text-muted">状态 {codex.state || '（空）'} · {rows.length} 行</span>
                </div>
                <pre className="mt-3 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-[6px] bg-surface-light p-3 font-mono text-detail leading-[1.7] text-text-dim">{rows.join('\n') || '（没有推送内容）'}</pre>
              </Panel>
            </div>
          ) : null}
        </div>
      </div>

      <div className="shrink-0 border-t border-border bg-surface">
        <div className="mx-auto flex w-full max-w-[1180px] items-center justify-between gap-4 px-5 py-2 text-subtitle">
          <span className={`truncate ${notice.error ? 'text-negative' : 'text-text-dim'}`}>{notice.text}</span>
          <span className="shrink-0 text-detail text-text-muted">v0.2.0-rc.1 · even-g2-go</span>
        </div>
      </div>
    </div>
  )
}

export default App
