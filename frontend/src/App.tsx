import { useEffect, useRef, useState, type ReactNode } from "react";
import { getTextWidth } from "@evenrealities/pretext";
import { Theme } from "@radix-ui/themes";
import {
  Badge,
  Button,
  Card,
  Divider,
  Input,
  Loading,
  SegmentedControl,
  StatusDot,
  Textarea,
  Toggle,
} from "./desktop-ui";
import {
  SlidersHorizontal as IcEditDisplayAdj,
  MonitorOff as IcFeatScreenOff,
  SunMoon as IcFeatTheme,
  ChevronDown as IcGuideChevronSmallDrillDown,
  Bluetooth as IcStatusBluetooth,
  BluetoothOff as IcStatusBluetoothDisconnected,
  Glasses as IcStatusGlasses,
  LayoutDashboard,
  Send,
  Settings2,
  Grid2X2,
  FlaskConical,
  ArrowUpRight,
  ArrowUp,
  ArrowDown,
} from "lucide-react";
import { EvenService } from "../bindings/even-glasses";
import { GameDemo } from "./GameDemo";
import { IMUDiagnosticsPanel } from "./IMUDiagnosticsPanel";
import type {
  CodexStatus,
  DashboardScheduleItem,
  DeviceCandidate,
  DeviceStatus,
} from "../bindings/even-glasses";

const initialStatus: DeviceStatus = {
  connected: false,
  connecting: true,
  leftState: "Scanning",
  rightState: "Scanning",
  lastError: "",
  lastErrorAt: "",
  reconnectCount: 0,
  displayDurationSeconds: 5,
  deviceId: "",
  deviceName: "",
  audioAvailable: false,
  settingsKnown: false,
  capabilities: {
    text: false,
    nativeText: false,
    nativeList: false,
    image: false,
    microphoneLC3: false,
    inputEvents: false,
    deviceSettings: false,
    brightness: false,
    headUp: false,
    screenPosition: false,
    dashboard: false,
  },
  settings: {
    batteryPercent: 0,
    charging: false,
    leftFirmwareVersion: "",
    rightFirmwareVersion: "",
    brightness: 0,
    autoBrightness: false,
    headUpEnabled: false,
    headUpAngle: 0,
    wearDetection: false,
    silentMode: false,
    screenDepth: 0,
    screenHeight: 0,
  },
  audioActive: false,
  audioFrames: 0,
  audioBytes: 0,
  lastEvent: { kind: "", name: "", itemName: "", type: "", itemIndex: 0 },
};

// Kept in step with stillStates in statusicon.go, including its shape. That one
// is an exception list on purpose: a state the Go side does not recognise counts
// as work in progress, so the lens fails towards showing motion rather than an
// empty icon. Listing the working states positively here would drift the moment
// an adapter invented a new one, and the preview would sit still while the lens
// was sweeping.
const stillStates = new Set([
  "needs_input",
  "permission",
  "paused",
  "done",
  "idle",
  "error",
  "failed",
]);

const capabilityLabels: Record<keyof DeviceStatus["capabilities"], string> = {
  text: "文本",
  nativeText: "原生文本",
  nativeList: "原生列表",
  image: "图片",
  microphoneLC3: "麦克风",
  inputEvents: "输入事件",
  deviceSettings: "设备信息",
  brightness: "亮度",
  headUp: "抬头显示",
  screenPosition: "屏幕位置",
  dashboard: "Dashboard",
};

// Display names only — the states themselves come from the hook adapters in
// scripts/. An unknown state falls through to its raw value, so a new adapter
// shows up as itself instead of as an empty label.
const stateLabels: Record<string, string> = {
  idle: "空闲",
  thinking: "思考中",
  tool: "调用工具",
  compacting: "整理上下文",
  needs_input: "等待输入",
  permission: "等待授权",
  paused: "已暂停",
  done: "已完成",
  error: "出错",
  failed: "失败",
};

// An empty state means no session has been pushed yet, which is not the same as
// an unrecognised one — nothing is running, so nothing should sweep.
const isWorkingState = (state: string) =>
  state !== "" && !stillStates.has(state);

type Notice = { text: string; error: boolean };

type SectionKey = "miniapps" | "overview" | "display" | "settings" | "labs";

const sections: { value: SectionKey; label: string; hint: string }[] = [
  { value: "overview", label: "首页", hint: "连接眼镜，开始使用" },
  { value: "miniapps", label: "Miniapp 管理", hint: "选择要显示在眼镜菜单里的应用" },
  {
    value: "display",
    label: "内容测试",
    hint: "把文字、列表或图片显示在眼镜上",
  },
  { value: "settings", label: "设备设置", hint: "亮度、抬头显示与屏幕位置" },
  { value: "labs", label: "开发者工具", hint: "用于调试和测试眼镜功能" },
];

type PreviewStyle = {
  X: number;
  Y: number;
  Width: number;
  Height: number;
  BorderWidth: number;
  BorderColor: number;
  BorderRadius: number;
  PaddingLength: number;
};

type PreviewBitmap = { X: number; Y: number; Width: number; Height: number; BMP: string };

type PreviewPage = {
  list?: { items: string[]; itemWidth: number; selectBorder: boolean };
  info?: { text: string; style: PreviewStyle };
  text: string;
  style: PreviewStyle;
  icon: PreviewBitmap | null;
  divider?: PreviewBitmap;
  overlays?: PreviewBitmap[];
  clock?: { Content: string; Geometry: { X: number; Y: number } }[];
};

const previewColour = (level: number) =>
  `rgba(155, 255, 159, ${Math.max(0, Math.min(15, level)) / 15})`;

function drawLensText(
  context: CanvasRenderingContext2D,
  text: string,
  x: number,
  y: number,
) {
  let cursor = x;
  for (const character of text) {
    context.fillText(character, cursor, y);
    cursor += getTextWidth(character);
  }
}

async function drawPreview(canvas: HTMLCanvasElement, page: PreviewPage) {
  const context = canvas.getContext("2d");
  if (!context) return;
  context.clearRect(0, 0, canvas.width, canvas.height);
  context.fillStyle = "#000";
  context.fillRect(0, 0, canvas.width, canvas.height);

  const style = page.style;
  if (style.BorderWidth > 0) {
    context.strokeStyle = previewColour(style.BorderColor);
    context.lineWidth = style.BorderWidth;
    context.beginPath();
    context.roundRect(
      style.X + style.BorderWidth / 2,
      style.Y + style.BorderWidth / 2,
      style.Width - style.BorderWidth,
      style.Height - style.BorderWidth,
      style.BorderRadius,
    );
    context.stroke();
  }
  if (page.list) {
    const inset = style.BorderWidth + style.PaddingLength;
    const itemX = style.X + inset;
    const itemY = style.Y + inset;
    context.save();
    context.beginPath();
    context.rect(style.X, style.Y, style.Width, style.Height);
    context.clip();
    context.font = '20px "PingFang SC", "SF Pro Text", sans-serif';
    context.textBaseline = "middle";
    // Firmware owns row layout and scrolling; Canvas approximates the initial list.
    page.list.items.forEach((label, index) => {
      const y = itemY + index * 40;
      if (index === 0 && page.list!.selectBorder) {
        context.strokeStyle = previewColour(15);
        context.lineWidth = 1;
        context.beginPath();
        context.roundRect(itemX + 1, y + 1, page.list!.itemWidth - 2, 38, 6);
        context.stroke();
      }
      context.fillStyle = previewColour(15);
      drawLensText(context, label, itemX + 12, y + 20);
    });
    context.restore();
    if (page.info) {
      const infoStyle = page.info.style;
      context.save();
      if (infoStyle.BorderWidth > 0) {
        context.strokeStyle = previewColour(infoStyle.BorderColor);
        context.lineWidth = infoStyle.BorderWidth;
        context.beginPath();
        context.roundRect(
          infoStyle.X + infoStyle.BorderWidth / 2,
          infoStyle.Y + infoStyle.BorderWidth / 2,
          infoStyle.Width - infoStyle.BorderWidth,
          infoStyle.Height - infoStyle.BorderWidth,
          infoStyle.BorderRadius,
        );
        context.stroke();
      }
      const infoInset = infoStyle.BorderWidth + infoStyle.PaddingLength;
      context.beginPath();
      context.rect(
        infoStyle.X + infoInset,
        infoStyle.Y + infoInset,
        infoStyle.Width - infoInset * 2,
        infoStyle.Height - infoInset * 2,
      );
      context.clip();
      context.fillStyle = previewColour(12);
      context.font = '20px "PingFang SC", "SF Pro Text", sans-serif';
      context.textBaseline = "top";
      page.info.text.split("\n").forEach((line, index) => {
        drawLensText(
          context,
          line,
          infoStyle.X + infoInset,
          infoStyle.Y + infoInset + index * 27,
        );
      });
      context.restore();
    }
  } else {
    const inset = style.BorderWidth + style.PaddingLength;
    context.save();
    context.beginPath();
    context.rect(
      style.X + inset,
      style.Y + inset,
      style.Width - inset * 2,
      style.Height - inset * 2,
    );
    context.clip();
    context.fillStyle = previewColour(15);
    context.font = '20px "PingFang SC", "SF Pro Text", sans-serif';
    context.textBaseline = "top";
    page.text.split("\n").forEach((line, index) => {
      drawLensText(
        context,
        line,
        style.X + inset,
        style.Y + inset + index * 27,
      );
    });
    context.restore();
  }

  async function drawBMP(
    item: PreviewBitmap,
    label: string,
  ) {
    const image = new Image();
    await new Promise<void>((resolve, reject) => {
      image.onload = () => resolve();
      image.onerror = () => reject(new Error(`无法解码${label}`));
      image.src = `data:image/bmp;base64,${item.BMP}`;
    });
    if (!page.overlays?.length) {
      context!.drawImage(image, item.X, item.Y, item.Width, item.Height);
      return;
    }
    const bitmap = document.createElement("canvas");
    bitmap.width = item.Width;
    bitmap.height = item.Height;
    const bitmapContext = bitmap.getContext("2d")!;
    bitmapContext.drawImage(image, 0, 0);
    const pixels = bitmapContext.getImageData(0, 0, bitmap.width, bitmap.height);
    for (let offset = 0; offset < pixels.data.length; offset += 4) {
      const brightness = pixels.data[offset];
      pixels.data.set([155, 255, 159, brightness], offset);
    }
    bitmapContext.putImageData(pixels, 0, 0);
    context!.drawImage(bitmap, item.X, item.Y);
  }
  if (page.divider?.BMP) {
    await drawBMP(page.divider, "右侧状态栏");
  }
  if (page.icon?.BMP) {
    await drawBMP(page.icon, "状态图标");
  }
  for (const overlay of page.overlays ?? []) {
    await drawBMP(overlay, "播放器图层");
  }
  context.fillStyle = previewColour(15);
  context.font = '20px "PingFang SC", "SF Pro Text", sans-serif';
  context.textBaseline = "top";
  for (const { Content, Geometry } of page.clock ?? []) {
    drawLensText(context, Content, Geometry.X, Geometry.Y);
  }
}

// The canvas renders whatever the service last handed to the glasses, including
// the BMP status icon, so the panel is a faithful readout and not a mock-up.
// It is only mounted while the preview is unfolded.
function LensCanvas() {
  const canvas = useRef<HTMLCanvasElement>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    async function refresh() {
      try {
        const page = JSON.parse(await EvenService.PreviewPage()) as PreviewPage;
        if (stopped) return;
        if (canvas.current) await drawPreview(canvas.current, page);
        setError("");
      } catch (error) {
        if (stopped) return;
        setError(String(error));
      }
      if (!stopped) timer = setTimeout(refresh, 250);
    }
    void refresh();
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, []);
  return (
    <div className="lens-simulator-shell">
      <div className="lens-display">
        <canvas
          ref={canvas}
          width={576}
          height={288}
          aria-label="内置眼镜画面预览"
        />
        {error && <div className="lens-empty">{error}</div>}
      </div>
    </div>
  );
}

function LensPanel({
  open,
  onToggle,
  summary,
  state,
  sweeping,
}: {
  open: boolean;
  onToggle: () => void;
  summary: string;
  state: string;
  sweeping: boolean;
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
            <span className="text-normal-title text-text">眼镜画面</span>
            {sweeping ? <Loading size={14} /> : null}
          </span>
          <span className="mt-0.5 block truncate text-detail text-text-dim">
            {summary}
          </span>
        </span>
        {state ? <Badge variant="neutral">{state}</Badge> : null}
        <IcGuideChevronSmallDrillDown
          className={`lens-fold-chevron${open ? " is-open" : ""}`}
          width={16}
          height={16}
        />
      </button>
      {open ? (
        <div className="lens-fold-body">
          <LensCanvas />
        </div>
      ) : null}
    </Card>
  );
}

function Panel({
  title,
  hint,
  action,
  className,
  children,
}: {
  title: string;
  hint?: ReactNode;
  action?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return (
    <Card className={`border border-border ${className ?? ""}`}>
      <div className="mb-3 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-normal-title text-text">{title}</h2>
          {hint ? (
            <p className="mt-1 text-detail leading-[1.55] text-text-dim">
              {hint}
            </p>
          ) : null}
        </div>
        {action ? <div className="shrink-0">{action}</div> : null}
      </div>
      {children}
    </Card>
  );
}

// Tiles carry the read-only values: arms, battery, counters. They sit on
// surface-light so a row of them reads as one grouped block.
function Tile({
  label,
  value,
  hint,
}: {
  label: string;
  value: ReactNode;
  hint?: ReactNode;
}) {
  return (
    <div className="min-w-0 rounded-[6px] bg-surface-light px-3 py-2">
      <div className="text-detail text-text-muted">{label}</div>
      <div className="mt-0.5 truncate text-subtitle text-text">{value}</div>
      {hint ? (
        <div className="mt-0.5 truncate text-detail text-text-muted">
          {hint}
        </div>
      ) : null}
    </div>
  );
}

// Keep the native picker with an explicit chevron in the desktop theme.
function DeviceSelect({
  label,
  value,
  onChange,
  children,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  children: ReactNode;
}) {
  return (
    <div className="relative">
      <select
        aria-label={label}
        className="h-9 w-full appearance-none rounded-[6px] bg-input-bg pl-3 pr-8 text-subtitle text-text outline-none"
        value={value}
        onChange={(event) => onChange(event.target.value)}
      >
        {children}
      </select>
      <IcGuideChevronSmallDrillDown
        className="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 text-text-dim"
        width={16}
        height={16}
      />
    </div>
  );
}

function SwitchRow({
  label,
  checked,
  onChange,
  disabled,
}: {
  label: string;
  checked: boolean;
  onChange: (value: boolean) => void;
  disabled?: boolean;
}) {
  return (
    <div className="flex items-center justify-between gap-3 py-1">
      <span className="text-subtitle text-text">{label}</span>
      <Toggle
        checked={checked}
        onChange={onChange}
        disabled={disabled}
        label={label}
      />
    </div>
  );
}

function App() {
  const [section, setSection] = useState<SectionKey>("overview");
  const workspaceScroll = useRef<HTMLDivElement>(null);
  useEffect(() => {
    workspaceScroll.current?.scrollTo({ top: 0 });
  }, [section]);
  const [previewOpen, setPreviewOpen] = useState(true);
  const [status, setStatus] = useState<DeviceStatus>(initialStatus);
  const [codex, setCodex] = useState<CodexStatus>({
    available: false,
    message: "正在连接状态桥",
    state: "",
    rows: [],
  });
  const [contentType, setContentType] = useState("text");
  const [text, setText] = useState("Hello from Even Glasses");
  const [list, setList] = useState("Today\nFocus mode\nTake a break");
  const [imageData, setImageData] = useState("");
  const [imageName, setImageName] = useState("");
  const [mode, setMode] = useState<"native" | "teleprompter">("native");
  const [displaySeconds, setDisplaySeconds] = useState(5);
  const [listBuddyMode, setListBuddyMode] = useState(() => {
    const saved = localStorage.getItem("listBuddyMode");
    return saved === "xiaojiaqi" ? saved : "qingzhu";
  });
  const [busy, setBusy] = useState(false);
  const [dark, setDark] = useState(false);
  const [notice, setNotice] = useState<Notice>({
    text: "准备连接你的 Even G2",
    error: false,
  });
  const [candidates, setCandidates] = useState<DeviceCandidate[]>([]);
  const [selectedLeft, setSelectedLeft] = useState("");
  const [selectedRight, setSelectedRight] = useState("");
  const [brightness, setBrightness] = useState(50);
  const [autoBrightness, setAutoBrightness] = useState(false);
  const [headUpEnabled, setHeadUpEnabled] = useState(false);
  const [headUpAngle, setHeadUpAngle] = useState(30);
  const [screenHeight, setScreenHeight] = useState(4);
  const [screenDepth, setScreenDepth] = useState(1);
  const [dashboardExperimental, setDashboardExperimental] = useState(false);
  const [dashboardHalfDay, setDashboardHalfDay] = useState(false);
  const [dashboardCelsius, setDashboardCelsius] = useState(true);
  const [scheduleJSON, setScheduleJSON] = useState(
    '[{"id":1,"title":"示例日程","location":"办公室","time":"10:00","endTimestamp":0}]',
  );

  // The theme attribute has to sit on <html>: the token aliases in theme.css are
  // declared on :root, so a value set further down would not reach them.
  useEffect(() => {
    document.documentElement.dataset.theme = dark ? "dark" : "light";
  }, [dark]);

  const refresh = async () => {
    try {
      const [device, bridge] = await Promise.all([
        EvenService.Status(),
        EvenService.CodexStatus(),
      ]);
      setStatus(device);
      setCodex(bridge);
    } catch (error) {
      console.error(error);
    }
  };

  useEffect(() => {
    void refresh();
    const timer = setInterval(() => void refresh(), 500);
    return () => clearInterval(timer);
  }, []);

  useEffect(() => {
    void EvenService.SetListBuddyMode(listBuddyMode).catch(console.error);
  }, []);

  useEffect(() => {
    if (!status.settingsKnown) return;
    setBrightness(status.settings.brightness);
    setAutoBrightness(status.settings.autoBrightness);
    setHeadUpEnabled(status.settings.headUpEnabled);
    setHeadUpAngle(status.settings.headUpAngle);
    setScreenHeight(status.settings.screenHeight);
    setScreenDepth(status.settings.screenDepth);
  }, [
    status.settingsKnown,
    status.settings.brightness,
    status.settings.autoBrightness,
    status.settings.headUpEnabled,
    status.settings.headUpAngle,
    status.settings.screenHeight,
    status.settings.screenDepth,
  ]);

  const run = async (task: () => Promise<unknown>, success: string) => {
    setBusy(true);
    try {
      await task();
      setNotice({ text: success, error: false });
      await refresh();
    } catch (error) {
      setNotice({
        text: error instanceof Error ? error.message : String(error),
        error: true,
      });
      await refresh();
    } finally {
      setBusy(false);
    }
  };

  const [miniApps, setMiniApps] = useState<
    NonNullable<Awaited<ReturnType<typeof EvenService.MiniAppVisibility>>>
  >([]);
  useEffect(() => {
    if (section !== "miniapps") return;
    void EvenService.MiniAppVisibility().then((apps) => setMiniApps(apps ?? [])).catch((error) => {
      setNotice({ text: String(error), error: true });
    });
  }, [section]);

  const setMiniAppVisible = (id: string, visible: boolean) =>
    run(async () => {
      try {
        await EvenService.SetMiniAppVisible(id, visible);
      } finally {
        setMiniApps((await EvenService.MiniAppVisibility()) ?? []);
      }
    }, "眼镜菜单显示设置已保存");

  const moveMiniApp = (id: string, up: boolean) =>
    run(async () => {
      try {
        await EvenService.MoveMiniApp(id, up);
      } finally {
        setMiniApps((await EvenService.MiniAppVisibility()) ?? []);
      }
    }, "Miniapp 排序已保存");

  const connect = () =>
    run(
      async () => setStatus(await EvenService.Connect()),
      "已开始主动搜索 Even G2",
    );
  const disconnect = () => run(EvenService.Disconnect, "已断开连接");
  const scanCandidates = () =>
    run(async () => {
      const found = (await EvenService.ScanCandidates()) ?? [];
      setCandidates(found);
      const left =
        found.find((candidate) => candidate.arm === "LEFT")?.address ?? "";
      const right =
        found.find((candidate) => candidate.arm === "RIGHT")?.address ?? "";
      setSelectedLeft(left);
      setSelectedRight(right);
      if (!left || !right)
        throw new Error("没有扫描到完整的左右镜腿，请确认眼镜已开机并靠近电脑");
    }, "扫描完成，请确认左右镜腿后连接");
  const connectSelected = () =>
    run(
      async () =>
        setStatus(await EvenService.ConnectDevice(selectedLeft, selectedRight)),
      "已连接所选 Even G2",
    );
  const clearDisplay = () => run(EvenService.ClearDisplay, "眼镜显示已清空");
  const saveBrightness = () =>
    run(async () => {
      await EvenService.SetBrightness(brightness, autoBrightness);
      await EvenService.RequestDeviceSettings();
    }, "亮度设置已发送");
  const saveHeadUp = () =>
    run(async () => {
      await EvenService.SetHeadUp(headUpEnabled, headUpAngle);
      await EvenService.RequestDeviceSettings();
    }, "抬头显示设置已发送");
  const saveScreenPosition = () =>
    run(async () => {
      await EvenService.SetScreenPosition(screenHeight, screenDepth);
      await EvenService.RequestDeviceSettings();
    }, "屏幕位置设置已发送");
  const startMicrophone = () =>
    run(EvenService.StartMicrophone, "眼镜麦克风已启动");
  const stopMicrophone = () =>
    run(EvenService.StopMicrophone, "眼镜麦克风已停止");
  const showDashboard = () => run(EvenService.ShowDashboard, "已返回眼镜主页");
  const configureDashboard = () =>
    run(
      () =>
        EvenService.ConfigureDashboard(
          [1, 2, 3, 4, 5],
          dashboardHalfDay,
          dashboardCelsius,
          dashboardExperimental,
        ),
      "Dashboard 配置已发送",
    );
  const pushSchedule = () =>
    run(async () => {
      const items = JSON.parse(scheduleJSON) as DashboardScheduleItem[];
      await EvenService.PushDashboardSchedule(items, dashboardExperimental);
    }, "Dashboard 日程已发送");
  const clearSchedule = () =>
    run(
      () => EvenService.ClearDashboardSchedule(dashboardExperimental),
      "Dashboard 日程已清空",
    );
  const saveDisplayDuration = () =>
    run(
      () => EvenService.SetDisplayDuration(displaySeconds),
      `显示时长已设为 ${displaySeconds} 秒`,
    );
  const chooseListBuddy = (mode: string) => {
    setListBuddyMode(mode);
    localStorage.setItem("listBuddyMode", mode);
    void run(() => EvenService.SetListBuddyMode(mode), "列表伙伴已更新");
  };
  const sendText = () =>
    run(
      () =>
        mode === "teleprompter"
          ? EvenService.ShowTeleprompter(text)
          : EvenService.ShowNativeText(text),
      mode === "teleprompter" ? "提词器内容已发送" : "全镜片原生文本已发送",
    );
  const sendList = () =>
    run(() => EvenService.ShowList(list.split("\n")), "可点击列表已发送到眼镜");
  const chooseImage = (file?: File) => {
    if (!file) return;
    setImageName(file.name);
    const reader = new FileReader();
    reader.onload = () =>
      setImageData(typeof reader.result === "string" ? reader.result : "");
    reader.readAsDataURL(file);
  };
  const sendImage = () =>
    run(() => EvenService.ShowImage(imageData), "图片已转换并发送到眼镜");
  const calibrate = () =>
    run(async () => {
      const result = await EvenService.LensCalibration();
      if (result?.length)
        setCodex((previous) => ({
          ...previous,
          rows: result,
          message: "镜片校准列表已推送（请在眼镜上查看读数）",
        }));
    }, "镜片校准列表已发送，请对照眼镜读数并反馈行宽 / 行数 / 符号");

  const rows = codex.rows ?? [];
  const sweeping = isWorkingState(codex.state);
  const connectionLabel = status.connected
    ? "已连接"
    : status.connecting
      ? "正在连接"
      : "未连接";
  const stateText = codex.state
    ? (stateLabels[codex.state] ?? codex.state)
    : "空闲";
  const activeSection =
    sections.find((item) => item.value === section) ?? sections[0];
  const enabledCapabilities = (
    Object.entries(status.capabilities) as [
      keyof DeviceStatus["capabilities"],
      boolean,
    ][]
  ).filter(([, enabled]) => enabled);

  // One line has to answer "is anything running, and what does the lens say".
  const previewSummary = !status.connected
    ? "眼镜未连接 · 等待任务页面发送"
    : !rows.length
      ? "当前没有活动页面"
      : sweeping
        ? `${stateText} · 状态图标正在更新 · ${rows.length} 行`
        : `${stateText} · ${rows.length} 行`;

  return (
    <Theme
      appearance={dark ? "dark" : "light"}
      accentColor="jade"
      grayColor="slate"
      radius="large"
      panelBackground="translucent"
      className="desktop-theme"
    >
      <div className="desktop-shell">
        <aside className="desktop-sidebar">
          <div className="sidebar-drag window-drag" />
          <div className="brand">
            <span className="brand-icon">
              <IcStatusGlasses size={25} />
            </span>
            <div>
              Even<span>G2 WORKSPACE</span>
            </div>
          </div>
          <p className="sidebar-label">工作空间</p>
          <nav aria-label="主导航" className="sidebar-nav">
            {sections
              .filter(
                (item) =>
                  item.value === "overview" || item.value === "miniapps" || item.value === "settings",
              )
              .map((item) => {
                const Icon =
                  item.value === "overview" ? LayoutDashboard :
                  item.value === "miniapps" ? Grid2X2 : Settings2;
                return (
                  <button
                    key={item.value}
                    type="button"
                    aria-current={section === item.value ? "page" : undefined}
                    className={`nav-item ${section === item.value ? "active" : ""}`}
                    onClick={() => setSection(item.value)}
                  >
                    <Icon size={18} />
                    <span>{item.label}</span>
                    {section === item.value && (
                      <span className="nav-indicator" />
                    )}
                  </button>
                );
              })}
          </nav>
          <div className="sidebar-device">
            <div className="sidebar-device-icon">
              <IcStatusGlasses size={32} />
            </div>
            <strong>{status.deviceName || "Even G2"}</strong>
            <span className="connection-caption">
              <StatusDot connected={status.connected} />
              {connectionLabel}
            </span>
            {status.settingsKnown && (
              <span className="battery-caption">
                电量 {status.settings.batteryPercent}%
                {status.settings.charging ? " · 充电中" : ""}
              </span>
            )}
          </div>
          <div className="sidebar-bottom">
            <span>桌面控制台</span>
            <span>G2</span>
          </div>
        </aside>
        <div className="desktop-workspace">
          <header className="workspace-toolbar window-drag">
            <span>
              Even Glasses <span className="toolbar-slash">/</span>{" "}
              {activeSection.label}
            </span>
            <div className="toolbar-actions">
              <span className="connection-pill">
                <StatusDot connected={status.connected} />
                {connectionLabel}
              </span>
              <Button
                variant="ghost"
                size="icon"
                aria-label={dark ? "切换到亮色主题" : "切换到暗色主题"}
                onClick={() => setDark((value) => !value)}
              >
                <IcFeatTheme size={18} />
              </Button>
            </div>
          </header>
          <div className="workspace-scroll" ref={workspaceScroll}>
            <div className="workspace-heading">
              <div>
                <h1>{activeSection.label}</h1>
                <p>{activeSection.hint}</p>
              </div>
              <span className="heading-glasses" aria-hidden="true">
                <IcStatusGlasses size={58} strokeWidth={1} />
              </span>
            </div>
            <div className="workspace-columns">
              <main
                className="workspace-content"
                id="main-content"
                key={section}
              >
                {section === "overview" ? (
                  <div className="grid items-start gap-4 content-grid">
                    <Panel
                      title={status.deviceName || "Even G2"}
                      action={
                        <Badge
                          variant={status.connected ? "positive" : "neutral"}
                        >
                          {connectionLabel}
                        </Badge>
                      }
                      hint={
                        status.connected
                          ? "已连接，AI 任务状态将同步到眼镜"
                          : "打开眼镜并靠近电脑，使用蓝牙连接"
                      }
                    >
                      {status.lastError && (
                        <p className="mt-3 text-subtitle text-negative">
                          连接遇到问题，请确认眼镜已开机并靠近电脑，再重新连接。
                        </p>
                      )}
                      <div className="mt-4 flex gap-2">
                        <Button
                          className="flex-1"
                          disabled={
                            busy || status.connected || status.connecting
                          }
                          onClick={connect}
                        >
                          <IcStatusBluetooth width={20} height={20} />
                          {status.connecting ? "正在连接…" : "连接眼镜"}
                        </Button>
                        {(status.connected || status.connecting) && (
                          <Button
                            variant="secondary"
                            disabled={busy}
                            onClick={disconnect}
                          >
                            <IcStatusBluetoothDisconnected
                              width={20}
                              height={20}
                            />
                            断开
                          </Button>
                        )}
                        {!status.connected && (
                          <Button
                            variant="ghost"
                            disabled={busy}
                            onClick={scanCandidates}
                          >
                            选择眼镜
                          </Button>
                        )}
                      </div>
                      {!status.connected ? (
                        <div className="mt-3 flex flex-col gap-2">
                          {candidates.length > 0 ? (
                            <div className="flex flex-col gap-2 rounded-[6px] bg-surface-light p-3">
                              <DeviceSelect
                                label="左镜腿"
                                value={selectedLeft}
                                onChange={setSelectedLeft}
                              >
                                <option value="">选择左镜腿</option>
                                {candidates
                                  .filter(
                                    (candidate) => candidate.arm === "LEFT",
                                  )
                                  .map((candidate) => (
                                    <option
                                      key={candidate.address}
                                      value={candidate.address}
                                    >
                                      {candidate.name} · {candidate.address} ·{" "}
                                      {candidate.rssi} dBm
                                    </option>
                                  ))}
                              </DeviceSelect>
                              <DeviceSelect
                                label="右镜腿"
                                value={selectedRight}
                                onChange={setSelectedRight}
                              >
                                <option value="">选择右镜腿</option>
                                {candidates
                                  .filter(
                                    (candidate) => candidate.arm === "RIGHT",
                                  )
                                  .map((candidate) => (
                                    <option
                                      key={candidate.address}
                                      value={candidate.address}
                                    >
                                      {candidate.name} · {candidate.address} ·{" "}
                                      {candidate.rssi} dBm
                                    </option>
                                  ))}
                              </DeviceSelect>
                              <Button
                                disabled={
                                  busy || !selectedLeft || !selectedRight
                                }
                                onClick={connectSelected}
                              >
                                连接所选镜腿
                              </Button>
                            </div>
                          ) : null}
                        </div>
                      ) : null}
                    </Panel>

                    <Panel title="日常操作">
                      <div className="quick-actions consumer-actions">
                        <button
                          type="button"
                          onClick={() => setSection("settings")}
                        >
                          <Settings2 size={19} />
                          <span>
                            调节眼镜显示<small>亮度、画面位置、抬头显示</small>
                          </span>
                          <ArrowUpRight size={15} />
                        </button>
                        <button
                          type="button"
                          disabled={busy || !status.connected}
                          onClick={showDashboard}
                        >
                          <LayoutDashboard size={19} />
                          <span>
                            返回眼镜主页<small>查看时间、天气等信息</small>
                          </span>
                          <ArrowUpRight size={15} />
                        </button>
                      </div>
                    </Panel>
                    <Panel
                      title="AI 任务同步"
                      action={
                        <Badge
                          variant={codex.available ? "positive" : "neutral"}
                        >
                          {codex.available ? "已接入" : "未接入"}
                        </Badge>
                      }
                    >
                      <p className="text-subtitle text-text-dim">
                        {codex.available
                          ? stateText
                          : "等待 Codex 或 Claude 任务接入"}
                      </p>
                      <p className="mt-2 text-detail text-text-muted">
                        在电脑上使用 Codex 或 Claude
                        时，可在眼镜上查看任务进度和完成提醒。
                      </p>
                    </Panel>
                  </div>
                ) : null}

                {section === "display" ? (
                  <div className="flex flex-col gap-4">
                    <Panel
                      title="发送到眼镜"
                      hint={
                        !status.connected ? "请先在首页连接眼镜" : undefined
                      }
                    >
                      <SegmentedControl
                        className="mb-4 w-full"
                        options={[
                          { value: "text", label: "文字" },
                          { value: "list", label: "列表" },
                          { value: "image", label: "图片" },
                        ]}
                        value={contentType}
                        onValueChange={setContentType}
                      />
                      {contentType === "text" && (
                        <>
                          <Textarea
                            rows={8}
                            value={text}
                            onChange={(event) => setText(event.target.value)}
                            placeholder="输入想在眼镜上看到的文字"
                            aria-label="文字内容"
                          />
                          <div className="composer-options">
                            <SwitchRow
                              label="长文提词器"
                              checked={mode === "teleprompter"}
                              onChange={(checked) =>
                                setMode(checked ? "teleprompter" : "native")
                              }
                            />
                            <span className="text-detail text-text-muted">
                              长文自动分页阅读
                            </span>
                          </div>
                        </>
                      )}
                      {contentType === "list" && (
                        <>
                          <p className="mb-3 text-subtitle text-text-dim">
                            每行一项，可在眼镜上点选。
                          </p>
                          <Textarea
                            rows={8}
                            value={list}
                            onChange={(event) => setList(event.target.value)}
                            placeholder="每行输入一个项目"
                            aria-label="列表内容"
                          />
                        </>
                      )}
                      {contentType === "image" && (
                        <label className="image-picker">
                          <input
                            type="file"
                            accept="image/png,image/jpeg"
                            aria-label="选择图片"
                            onChange={(event) =>
                              chooseImage(event.target.files?.[0])
                            }
                          />
                          {imageData ? (
                            <img src={imageData} alt="待发送图片" />
                          ) : (
                            <Send size={26} />
                          )}
                          <strong>{imageName || "点击选择图片"}</strong>
                          <span>支持 PNG、JPEG，自动适配眼镜画面</span>
                        </label>
                      )}
                      <div className="composer-footer">
                        <Button
                          disabled={
                            busy ||
                            !status.connected ||
                            (contentType === "text"
                              ? !text.trim()
                              : contentType === "image"
                                ? !imageData
                                : false)
                          }
                          onClick={
                            contentType === "text"
                              ? sendText
                              : contentType === "list"
                                ? sendList
                                : sendImage
                          }
                        >
                          <Send size={16} />
                          发送
                          {contentType === "text"
                            ? "文字"
                            : contentType === "list"
                              ? "列表"
                              : "图片"}
                        </Button>
                        <Button
                          variant="ghost"
                          disabled={busy || !status.connected}
                          onClick={clearDisplay}
                        >
                          <IcFeatScreenOff size={16} />
                          清空画面
                        </Button>
                      </div>
                    </Panel>
                    <Panel title="显示时长">
                      <div className="setting-inline">
                        <label htmlFor="display-seconds">内容保留</label>
                        <div>
                          <Input
                            id="display-seconds"
                            type="number"
                            min={1}
                            max={300}
                            className="w-20"
                            value={displaySeconds}
                            onChange={(event) =>
                              setDisplaySeconds(Number(event.target.value))
                            }
                          />
                          <span>秒</span>
                          <Button
                            variant="secondary"
                            disabled={
                              busy || displaySeconds < 1 || displaySeconds > 300
                            }
                            onClick={saveDisplayDuration}
                          >
                            保存时长
                          </Button>
                        </div>
                      </div>
                      <p className="mt-2 text-detail text-text-muted">
                        AI 任务运行期间保持显示，结束后开始计时。
                      </p>
                    </Panel>
                  </div>
                ) : null}

                {section === "miniapps" ? (
                  <div className="flex flex-col gap-4">
                  <Panel title="眼镜菜单" hint="用箭头调整顺序，关闭开关即可隐藏应用">
                    <div className="flex flex-col gap-3">
                      {miniApps.map((app, index) => (
                        <div key={app.id} className="flex items-center gap-2">
                        <div className="min-w-0 flex-1">
                        <SwitchRow
                          key={app.id}
                          label={app.name}
                          checked={app.visible}
                          disabled={busy}
                          onChange={(visible) => void setMiniAppVisible(app.id, visible)}
                        />
                        </div>
                        <Button variant="ghost" aria-label={`上移${app.name}`} disabled={busy || index === 0} onClick={() => void moveMiniApp(app.id, true)}>
                          <ArrowUp size={16} />
                        </Button>
                        <Button variant="ghost" aria-label={`下移${app.name}`} disabled={busy || index === miniApps.length - 1} onClick={() => void moveMiniApp(app.id, false)}>
                          <ArrowDown size={16} />
                        </Button>
                        </div>
                      ))}
                    </div>
                  </Panel>
                  <Panel title="贪吃蛇 Demo" hint="用电脑 WASD 控制方向，蛇会自动前进">
                    <GameDemo />
                  </Panel>
                  </div>
                ) : null}

                {section === "settings" ? (
                  <div className="grid items-start gap-4 content-grid">
                    <div className="flex flex-col gap-4">
                      <Panel title="亮度" hint="调节眼镜画面的明暗">
                        <div className="range-setting">
                          <input
                            type="range"
                            min={0}
                            max={100}
                            value={brightness}
                            disabled={autoBrightness}
                            aria-label="亮度"
                            onChange={(event) =>
                              setBrightness(Number(event.target.value))
                            }
                          />
                          <span>
                            {autoBrightness ? "自动" : `${brightness}%`}
                          </span>
                        </div>
                        <div className="mt-3">
                          <SwitchRow
                            label="自动亮度"
                            checked={autoBrightness}
                            onChange={setAutoBrightness}
                          />
                        </div>
                        <Button
                          variant="secondary"
                          size="sm"
                          className="mt-2 w-full"
                          disabled={busy || brightness < 0 || brightness > 100}
                          onClick={saveBrightness}
                        >
                          应用亮度
                        </Button>
                      </Panel>

                      <Panel title="抬头显示" hint="抬头时自动显示当前页面">
                        <SwitchRow
                          label="启用抬头显示"
                          checked={headUpEnabled}
                          onChange={setHeadUpEnabled}
                        />
                        <div className="mt-3 flex items-center justify-between gap-3">
                          <span className="text-subtitle text-text-dim">
                            抬头角度
                          </span>
                          <div className="flex items-center gap-2">
                            <Input
                              type="number"
                              min={0}
                              max={60}
                              className="h-9 w-24 text-subtitle"
                              value={headUpAngle}
                              onChange={(event) =>
                                setHeadUpAngle(Number(event.target.value))
                              }
                            />
                            <span className="text-subtitle text-text-dim">
                              度
                            </span>
                          </div>
                        </div>
                        <Button
                          variant="secondary"
                          size="sm"
                          className="mt-3 w-full"
                          disabled={busy || headUpAngle < 0 || headUpAngle > 60}
                          onClick={saveHeadUp}
                        >
                          应用抬头设置
                        </Button>
                      </Panel>

                      <Panel title="列表伙伴" hint="选择列表右侧的伙伴">
                        <DeviceSelect
                          label="列表伙伴"
                          value={listBuddyMode}
                          onChange={chooseListBuddy}
                        >
                          <option value="qingzhu">Qingzhu</option>
                          <option value="xiaojiaqi">小佳琪</option>
                        </DeviceSelect>
                      </Panel>
                    </div>

                    <div className="flex flex-col gap-4">
                      <Panel
                        title="屏幕位置"
                        hint="调整画面在视野中的高度和远近"
                      >
                        <label className="range-label" htmlFor="screen-height">
                          画面高度 <span>{screenHeight}</span>
                        </label>
                        <div className="range-setting">
                          <input
                            id="screen-height"
                            type="range"
                            min={0}
                            max={12}
                            value={screenHeight}
                            onChange={(event) =>
                              setScreenHeight(Number(event.target.value))
                            }
                          />
                        </div>
                        <p className="mb-2 mt-4 text-subtitle text-text-dim">
                          画面距离
                        </p>
                        <SegmentedControl
                          className="w-full"
                          size="small"
                          options={[
                            { value: "0", label: "近" },
                            { value: "1", label: "标准" },
                            { value: "2", label: "远" },
                          ]}
                          value={String(screenDepth)}
                          onValueChange={(value) =>
                            setScreenDepth(Number(value))
                          }
                        />
                        <Button
                          variant="secondary"
                          size="sm"
                          className="mt-3 w-full"
                          disabled={
                            busy ||
                            screenHeight < 0 ||
                            screenHeight > 12 ||
                            screenDepth < 0 ||
                            screenDepth > 2
                          }
                          onClick={saveScreenPosition}
                        >
                          应用屏幕位置
                        </Button>
                      </Panel>

                      <details className="developer-options desktop-card panel-padding">
                        <summary>
                          <FlaskConical size={15} />
                          开发者选项
                        </summary>
                        <p className="mt-3 text-detail text-text-muted">
                          调试与测试工具，日常使用无需开启。
                        </p>
                        <div className="developer-links">
                          <Button
                            variant="secondary"
                            onClick={() => setSection("display")}
                          >
                            <Send size={15} />
                            内容发送测试
                          </Button>
                          <Button
                            variant="secondary"
                            onClick={() => setSection("labs")}
                          >
                            <FlaskConical size={15} />
                            调试与诊断
                          </Button>
                        </div>
                        <Panel title="设备参数">
                          {status.settingsKnown ? (
                            <>
                              <div className="grid grid-cols-2 gap-2">
                                <Tile
                                  label="电量"
                                  value={`${status.settings.batteryPercent}%`}
                                  hint={
                                    status.settings.charging
                                      ? "充电中"
                                      : undefined
                                  }
                                />
                                <Tile
                                  label="佩戴检测"
                                  value={
                                    status.settings.wearDetection
                                      ? "已佩戴"
                                      : "未佩戴"
                                  }
                                />
                                <Tile
                                  label="静音模式"
                                  value={
                                    status.settings.silentMode ? "开启" : "关闭"
                                  }
                                />
                                <Tile
                                  label="固件"
                                  value={`L ${status.settings.leftFirmwareVersion || "未知"}`}
                                  hint={`R ${status.settings.rightFirmwareVersion || "未知"}`}
                                />
                              </div>
                              <Divider className="my-3" />
                              <div className="flex flex-col gap-2 text-subtitle text-text-dim">
                                <div className="flex justify-between">
                                  <span>设备名</span>
                                  <span className="text-text">
                                    {status.deviceName || "Even G2"}
                                  </span>
                                </div>
                                <div className="flex justify-between gap-4">
                                  <span className="shrink-0">设备 ID</span>
                                  <span className="break-all text-right text-text">
                                    {status.deviceId || "—"}
                                  </span>
                                </div>
                              </div>
                            </>
                          ) : status.connected ? (
                            <p className="py-6 text-center text-subtitle text-text-dim">
                              正在读取设备设置…
                            </p>
                          ) : (
                            <p className="py-6 text-center text-subtitle text-text-muted">
                              连接眼镜后显示
                            </p>
                          )}
                        </Panel>
                      </details>
                    </div>
                  </div>
                ) : null}

                {section === "labs" ? (
                  <div className="grid items-start gap-4 content-grid">
                    <Panel title="IMU 诊断" hint="查看眼镜三轴报告，不改变镜腿操作">
                      <IMUDiagnosticsPanel connected={status.connected} />
                    </Panel>
                    <Panel title="官方模拟器" hint="在电脑上测试眼镜画面与交互">
                      <Button
                        variant="secondary"
                        disabled={busy}
                        onClick={() =>
                          run(
                            EvenService.StartNativeSimulator,
                            "官方模拟器已启动",
                          )
                        }
                      >
                        打开官方模拟器
                      </Button>
                    </Panel>
                    <Panel title="设备连接信息">
                      {" "}
                      <details className="compact-details mt-3">
                        <summary>连接详情</summary>
                        <div className="mt-3 grid grid-cols-2 gap-2">
                          <Tile label="左臂" value={status.leftState} />
                          <Tile label="右臂" value={status.rightState} />
                        </div>
                        {status.connected ? (
                          <p className="mt-3 break-all text-detail text-text-muted">
                            {status.deviceName || "Even G2"} · {status.deviceId}{" "}
                            · 麦克风{status.audioAvailable ? "可用" : "不可用"}
                          </p>
                        ) : null}
                        {status.connected && enabledCapabilities.length ? (
                          <div className="mt-3 flex flex-wrap gap-1">
                            {enabledCapabilities.map(([name]) => (
                              <Badge key={name}>{capabilityLabels[name]}</Badge>
                            ))}
                          </div>
                        ) : null}
                      </details>
                    </Panel>
                    <Panel
                      title="眼镜主页"
                      hint="返回眼镜自带的时间、天气等信息页"
                    >
                      <Button
                        className="w-full"
                        disabled={busy || !status.connected}
                        onClick={showDashboard}
                      >
                        返回眼镜主页
                      </Button>
                      <Divider className="my-3" />
                      <details className="compact-details">
                        <summary>高级主页设置</summary>
                        <SwitchRow
                          label="启用实验功能"
                          checked={dashboardExperimental}
                          onChange={setDashboardExperimental}
                        />
                        {dashboardExperimental ? (
                          <div className="mt-3 flex flex-col gap-2">
                            <SwitchRow
                              label="12 小时制"
                              checked={dashboardHalfDay}
                              onChange={setDashboardHalfDay}
                            />
                            <SwitchRow
                              label="摄氏温度"
                              checked={dashboardCelsius}
                              onChange={setDashboardCelsius}
                            />
                            <Button
                              variant="secondary"
                              className="mt-1"
                              disabled={busy || !status.connected}
                              onClick={configureDashboard}
                            >
                              应用默认组件排序
                            </Button>
                            <Textarea
                              rows={4}
                              value={scheduleJSON}
                              onChange={(event) =>
                                setScheduleJSON(event.target.value)
                              }
                            />
                            <div className="flex gap-2">
                              <Button
                                variant="secondary"
                                className="flex-1"
                                disabled={busy || !status.connected}
                                onClick={pushSchedule}
                              >
                                写入日程
                              </Button>
                              <Button
                                variant="secondary"
                                className="flex-1"
                                disabled={busy || !status.connected}
                                onClick={clearSchedule}
                              >
                                清空日程
                              </Button>
                            </div>
                          </div>
                        ) : null}
                      </details>
                    </Panel>

                    <Panel
                      title="镜片校准"
                      hint="发送测试页面，检查眼镜上的文字排版"
                    >
                      <Button
                        variant="secondary"
                        disabled={busy || !status.connected}
                        onClick={calibrate}
                      >
                        <IcEditDisplayAdj size={16} />
                        发送校准页面
                      </Button>
                    </Panel>
                    <Panel
                      title="麦克风与输入"
                      hint="查看录音数据和最近的眼镜操作"
                    >
                      <div className="flex gap-2">
                        <Button
                          disabled={
                            busy ||
                            !status.connected ||
                            !status.audioAvailable ||
                            status.audioActive
                          }
                          onClick={startMicrophone}
                        >
                          开始录音
                        </Button>
                        <Button
                          variant="secondary"
                          disabled={busy || !status.audioActive}
                          onClick={stopMicrophone}
                        >
                          停止录音
                        </Button>
                      </div>
                      <details className="compact-details mt-3">
                        <summary>查看录音与输入数据</summary>
                        <div className="mt-3 grid grid-cols-2 gap-2">
                          <Tile label="音频帧" value={status.audioFrames} />
                          <Tile
                            label="音频数据"
                            value={`${(status.audioBytes / 1024).toFixed(1)} KiB`}
                          />
                        </div>
                        <p className="mt-3 text-detail text-text-dim">
                          {status.lastEvent.kind
                            ? `${status.lastEvent.kind} · ${status.lastEvent.type} · ${status.lastEvent.name || "系统事件"} ${status.lastEvent.itemName} #${status.lastEvent.itemIndex}`
                            : "尚未收到眼镜操作"}
                        </p>
                      </details>
                    </Panel>
                    <Panel
                      title="连接诊断"
                      hint="自动重连与最近一次异常的原始信息"
                    >
                      <div className="grid grid-cols-3 gap-2">
                        <Tile label="连接状态" value={connectionLabel} />
                        <Tile
                          label="自动重连"
                          value={`${status.reconnectCount} 次`}
                        />
                        <Tile
                          label="最近异常"
                          value={status.lastErrorAt || "无"}
                          hint={status.lastError || undefined}
                        />
                      </div>
                      {status.lastError ? (
                        <p className="mt-3 break-words rounded-[6px] bg-negative-alpha px-3 py-2 text-detail text-negative">
                          {status.lastError}
                        </p>
                      ) : null}
                    </Panel>

                    <Panel
                      title="任务同步诊断"
                      hint="查看同步状态与发送到眼镜的文字"
                      className="content-span"
                    >
                      <div className="flex items-center gap-3">
                        <Badge
                          variant={codex.available ? "positive" : "negative"}
                        >
                          {codex.available ? "已接入" : "未接入"}
                        </Badge>
                        <span className="text-detail text-text-muted">
                          状态 {codex.state || "（空）"} · {rows.length} 行
                        </span>
                      </div>
                      <details className="compact-details mt-3">
                        <summary>查看原始内容</summary>
                        <pre className="mt-3 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-[6px] bg-surface-light p-3 font-mono text-detail leading-[1.7] text-text-dim">
                          {rows.join("\n") || "（没有推送内容）"}
                        </pre>
                      </details>
                    </Panel>
                  </div>
                ) : null}
              </main>
              <aside className="preview-rail" aria-label="实时眼镜预览">
                <div className="rail-heading">
                  <span>镜片实况</span>
                  <span className="live-label">
                    <i /> LIVE
                  </span>
                </div>
                <LensPanel
                  open={previewOpen}
                  onToggle={() => setPreviewOpen((value) => !value)}
                  summary={previewSummary}
                  state={codex.state ? stateText : ""}
                  sweeping={status.connected && sweeping}
                />
                <p className="preview-caption">实时预览 · 实际效果以眼镜为准</p>
              </aside>
            </div>
          </div>
          <footer className="workspace-footer" role="status" aria-live="polite">
            <span className={notice.error ? "text-negative" : ""}>
              <span className={`footer-dot ${notice.error ? "error" : ""}`} />
              {notice.text}
            </span>
            <span>v0.2.0-rc.1</span>
          </footer>
        </div>
      </div>
    </Theme>
  );
}

export default App;
