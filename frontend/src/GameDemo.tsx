import { useRef, useState } from "react";
import { EvenService } from "../bindings/even-glasses";
import { Button } from "./desktop-ui";

export function GameDemo() {
  const [message, setMessage] = useState("点击开始，再在控制区内按 WASD 转向");
  const control = useRef<HTMLDivElement>(null);
  const sending = useRef(false);

  const restart = async () => {
    try {
      await EvenService.GameStart();
      setMessage("W 上 · A 左 · S 下 · D 右，R 重新开始。▦ 蛇身，◆ 食物。碰墙或自身结束");
      control.current?.focus();
    } catch (error) { setMessage(String(error)); }
  };

  return (
    <div className="flex flex-col gap-3">
      <Button onClick={restart}>开始 / 重新开始（R）</Button>
      <div
        ref={control}
        tabIndex={0}
        role="group"
        aria-label="WASD 游戏控制区"
        className="rounded-xl border border-current p-4 focus:outline focus:outline-2"
        onKeyDown={async (event) => {
          const key = event.key.toLowerCase();
          if (!['w', 'a', 's', 'd', 'r'].includes(key) || event.metaKey || event.ctrlKey || event.altKey) return;
          event.preventDefault();
          if (key === 'r') {
            if (!event.repeat) await restart();
            return;
          }
          if (sending.current) return;
          sending.current = true;
          try { await EvenService.GameInput(key); }
          catch (error) { setMessage(String(error)); }
          finally { sending.current = false; }
        }}
      >{message}</div>
      <p className="text-detail text-text-muted">画面显示在右侧实时预览，连接眼镜后同步显示。眼镜内双击退出。</p>
    </div>
  );
}
