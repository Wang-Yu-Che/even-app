import { useEffect, useRef, useState } from "react";
import { EvenService, type IMUDiagnostics } from "../bindings/even-glasses";
import { Button } from "./desktop-ui";

const axes = [
  { key: "x", color: "#ef4444" },
  { key: "y", color: "#22c55e" },
  { key: "z", color: "#3b82f6" },
] as const;

export function IMUDiagnosticsPanel({ connected }: { connected: boolean }) {
  const [data, setData] = useState<IMUDiagnostics>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const command = useRef<Promise<void>>(Promise.resolve());

  useEffect(() => {
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    async function poll() {
      try {
        const snapshot = await EvenService.GetIMUDiagnostics();
        if (!disposed) setData(snapshot);
      } catch (err) {
        if (!disposed) setError(String(err));
      }
      if (!disposed) timer = setTimeout(poll, 250);
    }
    void poll();
    return () => {
      disposed = true;
      clearTimeout(timer);
    };
  }, []);

  useEffect(() => () => {
    void command.current.then(() => EvenService.SetIMUDiagnostics(false)).catch(console.error);
  }, []);

  function toggle() {
    setBusy(true);
    setError("");
    command.current = EvenService.SetIMUDiagnostics(!data?.active).then(async () => {
      setData(await EvenService.GetIMUDiagnostics());
    }).catch(err => setError(String(err))).finally(() => setBusy(false));
  }

  const samples = data?.samples ?? [];
  const latest = samples[samples.length - 1];
  const previous = samples[samples.length - 2];
  const start = samples[0]?.millis ?? 0;
  const span = Math.max(1, (latest?.millis ?? 0) - start);
  const scale = Math.max(1, ...samples.flatMap(sample => axes.map(axis => Math.abs(sample[axis.key]))));

  return (
    <div className="grid gap-3">
      <div>
        <Button variant="secondary" disabled={!connected || busy || !data} onClick={toggle}>
          {data?.active ? "停止采集" : "开始采集"}
        </Button>
      </div>
      {error && <p role="alert" className="text-detail">{error}</p>}
      {!latest ? (
        <p className="text-detail text-text-muted">{data?.active ? "等待眼镜报告…" : "连接眼镜后开始采集。"}</p>
      ) : (
        <>
          <p className="text-detail text-text-muted">
            {data?.active ? "采集中" : "采集已停止"} · 最近 {samples.length} 条 · 最新接收间隔 {previous ? `${(latest.millis - previous.millis).toFixed(0)} ms` : "—"}
          </p>
          <div className="flex flex-wrap gap-3 text-detail">
            {axes.map(({ key, color }) => <span key={key} style={{ color }}>{key.toUpperCase()} {latest[key].toFixed(3)}</span>)}
          </div>
          <svg viewBox="0 0 320 100" width="100%" role="img" aria-label="三轴 IMU 曲线，横轴为接收时间">
            <line x1="0" y1="50" x2="320" y2="50" stroke="currentColor" opacity="0.2" />
            {axes.map(({ key, color }) => (
              <polyline key={key} fill="none" stroke={color} strokeWidth="2"
                points={samples.map(sample => `${(sample.millis - start) / span * 320},${50 - sample[key] / scale * 46}`).join(" ")} />
            ))}
          </svg>
        </>
      )}
      <p className="text-detail text-text-muted">试着慢慢左右转头、点头和歪头，对比三轴变化。</p>
    </div>
  );
}
