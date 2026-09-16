# even-g2-go ↔ Even-G2-RE 能力比对

比对日期：2026-09-11
比对对象：

- 本地方：`github.com/Wang-Yu-Che/even-g2-go v0.1.0`（module cache，vendor 自 OpenEvenSdk）
- 外部：`https://github.com/lonelyobserver0/Even-G2-RE`（克隆于 `/tmp/even-g2-re`）
- 外部仓库基线：`REPORT_BLE_PROTOCOL.md`（firmware 2.0.7.16 实测）、`LIBAPP_DASHBOARD_RE_FINDINGS.md`、
  `DASHBOARD_PROTO_MAP.md`、`CURRENT_RE_FINDINGS.md`、`DASHBOARD_OUTBOUND_MODEL.md`

## 结论速览

- 传输层：两边描述完全一致，**唯一分歧只是 byte[1] 的方向位**，不是冲突。
- 显示渲染：SDK 明显领先（已真机出图、出列表、收点击），RE 仓库卡在 cmdId=8 字段映射上。
- 服务面：RE 仓库宽得多（dashboard、DEV_INFO、OTA、EFS、Ring、导航、翻译…），SDK 只覆盖
  OpenEvenSdk 已公开的子集。
- 最值得马上抄过来的两块：**serviceId 注册表 + DEV_INFO(265)** 和 **RLE 压缩**。

---

## 1. 传输层比对

### 1.1 帧结构：一致

| 位置 | SDK（OpenEvenSdk 硬件验证） | RE 仓库 | 是否一致 |
|---|---|---|---|
| byte[0] | `0xAA` magic | `0xAA` headId | 一致 |
| byte[1] | `0x21`（发）/ `0x12`（收） | `0x12`（称为唯一真实格式） | **见 1.2** |
| byte[2] | sequence | "varies，疑似 hash/counter" | 同一字段 |
| byte[3] | `len(payload)+2` | `payloadLen` | 数值恒等，见 1.3 |
| byte[4..5] | 分片总数 / 分片序号，单片为 `01 01` | 当成常量 `01 01` | **RE 漏了多分片语义** |
| byte[6..7] | service 两字节原样写入 | serviceId uint16 LE | 数值口径不同，见 1.4 |
| 尾部 2 字节 | CRC-16/CCITT-FALSE，只覆盖 payload，小端 | 同 | 一致 |
| 总长 | `8 + len(payload) + 2` | `payloadLen + 8` | 等价 |

### 1.2 关于 "AA 12 才是真实格式"

**这不是矛盾，是采样方向导致的。** 证据链：

1. RE 仓库自己写明，他们的样本全部来自 notify 特性 `...5402`（眼镜 → App 的下行）。
2. SDK 的 `g2/evenhub.go HandleNotification` 第一行就写着：
   `G2 replies use AA 12 while requests use AA 21`，收包只校验 `data[0]==0xAA`。
3. RE 自己给的 `DEV_INFO` 样例 protobuf 是 `field1=2`（cmdId=2）+ `field2=2`（seqNum），
   形态上就是一个响应包。

**结论：`AA 21` = App → 眼镜，`AA 12` = 眼镜 → App。** RE 把下行帧的 byte[1] 当成了通用格式。
如果要做他们的 Python sender，`AA 21` 的 auth 路径是对的，出站也该用 `0x21`。

### 1.3 byte[3] 的口径差异（无实质影响）

RE 把 service 两字节算进 payload，CRC 不算；SDK 把 service 算进 header，CRC 算进 len。
两边推导出的 byte[3] 恒等。已用 RE 的 `DEV_INFO` 样例验证：整包 51 字节，`byte[3]=0x2B=43`，
两种读法都成立，整包长度也一致。

### 1.4 service 两字节的字节序（**唯一值得实机核对的一点**）

- SDK：`BuildPacket(seq, serviceHi, serviceLo, ...)` → byte[6]=serviceHi，byte[7]=serviceLo 原样写。
- RE：`int.from_bytes(data[6:8], 'little')` → byte[6] 是低字节。

同一个 `DEV_INFO` 样例 `09 01`：RE 读成 265；SDK 口径读成 `0x0901`。
RE 表里那几个"好看"的 serviceId（128 / 264 / 265 / 269 / 384）都来自小端读法：

| RE 值 | 线上字节 6,7 | RE 名字 |
|---|---|---|
| 1 | `01 00` | UI_DEFAULT_APP_ID |
| 8 | `08 00` | UI_BACKGROUND_DASHBOARD_APP_ID |
| 12 | `0C 00` | SERVICE_MODULE_CONFIGURE_APP_ID |
| 14 | `0E 00` | UI_TRANSLATE_APP_ID |
| 128 | `80 00` | SYSTEM |
| 264 | `08 01` | SERVICE_SYNC_INFO_APP_ID |
| 265 | `09 01` | DEV_INFO |
| 269 | `0D 01` | UX_DEVICE_SETTINGS_APP_ID |
| 384 | `80 01` | UI_BACKGROUND_NAVIGATION_ID |

SDK 侧对应关系：认证 / 心跳 = `80 00`（= RE 的 128 SYSTEM ✓），EvenHub = `E0 00`（收）/ `E0 20`（发），
teleprompter = `06 20` / `0E 20`。

**待核对动作**：抓一次官方 App 起 EvenHub 的包，看 byte[6..7] 到底是 `E0 00` 还是别的。
另注意 SDK 的 `ServiceHi` / `ServiceLo` 命名在"小端读法"下是反的 —— 这只是命名问题，
但接 RE 的表时容易踩坑，建议在 SDK 里补一条注释或改名为 `ServiceByte` / `SubType`。

### 1.5 psType 多通道

| psType | SDK | RE |
|---|---|---|
| 0 → 主写 5401 / 通知 5402 | 用（控制 + EvenHub 图片） | 实测确认，全部数据流走这里 |
| 1 → 6401 / 6402 | 只用 6402 订阅麦克风 | 确认（6401 疑似 OTA / 文件） |
| 2 → 7401 / 7402 | 未用 | 确认存在 |
| 3 → 0x0882 / 0x0884 | 未用 | 实测发现，UUID 未确认 |

附带信息：RE 确认 `WRITE_TYPE_NO_RESPONSE`、默认 MTU 247、`psType=1` 豁免升级锁。
这些 SDK 行为一致但没有显式文档化。

### 1.6 多分片与重传

- SDK：`protocol.FrameEvenHub` 单片 1..255 片，共用 sequence，CRC 附在最后一片；
- 图片路径另有 3800 字节数据片 + 4 ACK 滑动窗口 + 块间心跳 + session 跳跃重试（真机验证）。
- RE：只观察到单片 `01 01`，把 byte[4..5] 当常量；transport 有 `_splitDataIntoPackets` /
  `_waitForPacketResponse` / `_parseRetryPackets` 的字符串，但没还原出实现。

**这是 SDK 可以反向输出给 RE 的部分。**

---

## 2. 显示与渲染

### 2.1 SDK 已实现（真机验证）

- 4bpp 灰度 BMP 576×288（16 级调色板），2×2 切片 288×144；
- EvenHub serviceId `0xE0` 上的 Cmd=1 建容器 / Cmd=3 图片分片 / Cmd=5 文本更新 /
  Cmd=7 重建 / Cmd=9 关闭；
- 列表容器逐行可点击、文本容器点击、上下滑翻页、双击，事件语义化回调；
- teleprompter 25 列 × 10 行 × 14 页，左右臂镜像写入。

### 2.2 RE 有、SDK 没有

| 项 | RE 的证据 | 影响 |
|---|---|---|
| **Dashboard 通道** | serviceId=8，cmdId=7 建页 / cmdId=8 图更，占总流量 65% | 官方 Dashboard 无法复刻 |
| **protobuf 家族** | `DashboardDataPackage` → `AppRequest` → `CreateStartUpPageContainer` / `RebuildPageContainer` / `ProtoImage` / `ProtoUpdateWithImageCallArguments` | 结构比想象中深 |
| **语义卡片** | `DashboardContent` oneof：`singleData` / `multData` / `singleHighlight` / `multHighlight`；`sendWeatherInfoToGlass` / `sendStockContentToGlass` / `sendNewsContentToGlass` / `sendCalendarContentToGlass` | 天气 / 股票 / 新闻 / 日历卡片 |
| **布局元数据** | `DashboardDisplaySetting{gridDistance, gridHeight}` | 建页必须带真实几何 |
| **有状态页面** | OS 响应回传 `pageId` / `lineId`，App 维护之 | SDK 现在是无状态 magic 关联 |
| **RLE 压缩** | `compressBmpData` + `_EvenRleCompressor` | SDK 发未压缩 4bpp，单块 41472 字节 |
| **更多编码模式** | 1bpp / 4bpp / 8bpp + RLE4/RLE8/paletteRle/grayRle | SDK 只有 4bpp |

### 2.3 关于 Dashboard 的现状（不要期待过高）

RE 仓库自己承认这条路**没打通**：`NEXT_PATHS_SUMMARY.md` 明确说
"不要再用猜字段的 sender 变体浪费时间"，`DASHBOARD_PAYLOAD_CONSTRAINTS.md` 列出
四条硬约束 —— 关键结论是：

- 他们抓到的 `serviceId=8, cmdId=8` 是**下行 ACK**（`field1=8, field2=seqNum, field6=""`），
  不能用它推 `field6` 是图片字段；
- `cmdId=7` 需要真实的页/容器元数据，空包不会被渲染；
- EvenHub 的 `image_data` / `container_id` 只是**概念提示**，不代表 dashboard 的字段号。

所以 **不建议现在跟进 Dashboard**，除非拿到官方 App 的真实出站字节。

---

## 3. 其它模块

| 模块 | RE | SDK |
|---|---|---|
| 七包认证 | 承认 legacy `AA 21` 路径 | 已实现（真机） |
| 心跳 | 空 payload 的 8 字节包，约 5-6 秒一次 | 已实现，但语义不同：SDK 发 `08 25`（service `80 00`）+ EvenHub `Cmd=12` |
| 麦克风 | 无 | 独立 6402 订阅 + LC3 5×40B 拆帧 + liblc3 解码 WAV（真机） |
| DEV_INFO 265 | 已解码样例：固件版本 / 电量 / 亮度 | **无** |
| serviceId 注册表 | 约 40 个符号名，10 个确认数值 | **无此概念** |
| OTA 固件升级 | `BleG2OtaHeader`、magic、component、CRC32 | 无 |
| 文件服务 EFS | SEND/EXPORT 各 2 个 serviceId + 6 条命令 | 无 |
| Ring R1 中继 | relay / raw / file / broadcast | 无 |
| 眼镜盒 / pairing / 导航 / 翻译 / 系统告警 | 有名字 | 无 |

---

## 4. 建议的落地顺序

按性价比排序（投入小 → 大）：

1. **DEV_INFO(265) 读取** —— 成本最低、收益即时。SDK 已有 proto 解析和收包路由，
   加一个请求 + 一个 `walkProto` 解析就有电量 / 固件版本 / 亮度。
2. **serviceId 常量表** —— 把 RE 那张表搬成 Go 常量 + 注释，顺带把
   `ServiceHi/ServiceLo` 的命名歧义澄清（见 1.4）。
3. **`AA 12` 下行帧在文档与命名上显式化** —— 现在只存在于 `HandleNotification` 的一行注释里。
4. **RLE 压缩** —— 先实现标准 RLE4/RLE8 试；`_EvenRleCompressor` 是专有的，
   需要用官方 App 的包对比验证。收益是图片传输时间显著下降。
5. **Dashboard（serviceId=8）** —— 等 RE 仓库或其他来源拿到真实出站字节再做。
6. **pageId/lineId 状态机** —— 若做 5 则必须先做。
7. OTA / EFS / Ring —— 明确超出现有范围，暂不做。

## 5. 可以反向给 RE 仓库的信息

- 出站帧 byte[1] 是 `0x21`，下行才是 `0x12`；他们的 Python sender 出站应该用 `0x21`。
- byte[4..5] 是**分片总数 / 分片序号**，不是常量；`01 01` 只是单片情况。
- EvenHub 服务的两字节是 `E0 00`（收）/ `E0 20`（发）。
- 可工作的图片路径存在：EvenHub `Cmd=1/3` + 3800 字节分片 + 4 ACK 滑窗 + 块间心跳，
  已在真机出图；不必只在 dashboard 的 cmdId=8 上死磕。
