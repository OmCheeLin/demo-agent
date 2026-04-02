# mini-tmk-agent-go

Go 主工程 + Python DashScope 网关版本（跨平台：Windows/Linux/macOS）。

## 运行前准备（按顺序）

- Go 1.22+
- Python 3.10+
- ffmpeg（用于 wav/mp3 解码与 stream 麦克风采集）
- ffplay（用于 stream 模式 TTS 播放）
- 安装 Python 依赖：

```bash
cd mini-tmk-agent-go
pip install dashscope
```

- 检查环境是否可用（这一步不要跳过）：

```powershell
go version
python --version
ffmpeg -version
ffplay -version
```

- 配置密钥：

```powershell
$env:DASHSCOPE_API_KEY=""
```

```bash
export DASHSCOPE_API_KEY=""
```

如需 TTS：

```powershell
$env:SILICONFLOW_API_KEY=""
```

```bash
export SILICONFLOW_API_KEY=""
```

## 构建

```bash
go build -o mini-tmk-agent.exe . # windows
go build -o mini-tmk-agent . # Linux
```

## 支持语言

当前参数校验支持：`zh/en/es/ja`

## 流式模式（先取设备名，再运行）

先按系统确认麦克风设备（推荐）：

- Windows（dshow）：

```powershell
ffmpeg -list_devices true -f dshow -i dummy
```

- Linux（pulse）：

```bash
pactl list short sources
```

- macOS（avfoundation）：

```bash
ffmpeg -f avfoundation -list_devices true -i ""
```

然后把设备名（或设备编号）填入 `--mic-device`：

```bash
./mini-tmk-agent stream --source-lang zh --target-lang en --mic-device default
```

启用 TTS（分段播报能力）：

```bash
./mini-tmk-agent stream --source-lang zh --target-lang en --mic-device default --enable-tts --tts-speed 1.0 --tts-threshold 8
```

## Transcript 模式

```bash
.\mini-tmk-agent.exe transcript --file D:\go\workspace\mini-tmk-agent\mini-tmk-agent-go\test_data\中文.pcm --output .\out.txt --source-lang zh --target-lang en

.\mini-tmk-agent.exe transcript --file D:\go\workspace\mini-tmk-agent\mini-tmk-agent-go\test_data\英语演讲.wav --output .\out.txt --source-lang en --target-lang zh

.\mini-tmk-agent.exe transcript --file D:\go\workspace\mini-tmk-agent\mini-tmk-agent-go\test_data\英语演讲.mp3 --output .\out.txt --source-lang en --target-lang zh
```

## 代码原理与流程

### 架构分层

- **Go 主程序（`main.go`）**：负责命令行参数、流程编排、音频采集/解码、输出打印、结果落盘。
- **Python 网关（`python_gateway/gateway.py`）**：负责对接 DashScope 实时识别与翻译 SDK。
- **进程通信方式**：Go 通过 `stdin/stdout` 与 Python 子进程通信。
  - Go -> Python：持续写入 PCM 音频帧（二进制）。
  - Python -> Go：逐行输出 JSON 事件（`status/sentence/word/final/error`）。

### stream 流程（实时麦克风）

1. Go 解析 `stream` 参数，拉起 Python 网关子进程。
2. Go 按平台调用 ffmpeg 采集麦克风音频（Windows: `dshow`，Linux: `pulse`，macOS: `avfoundation`），并转成 `s16le/mono/16000` PCM 流。
3. Go 将 PCM 持续写入 Python 的 `stdin`。
4. Python 网关把音频帧送入 DashScope `TranslationRecognizerRealtime`。
5. Python 将识别/翻译结果按事件 JSON 输出到 `stdout`。
6. Go 读取事件并打印 `[source]/[target]`；如启用 `--enable-tts`，将 `word` 事件送入 TTS worker，按句尾或标点阈值分段播放。

### transcript 流程（文件转写）

1. Go 解析 `transcript` 参数，按输入后缀处理音频：
   - `.pcm`：直接流式读取；
   - `.wav/.mp3`：用 ffmpeg 转成 `s16le/mono/16000` PCM。
2. Go 将 PCM 写入 Python 网关 `stdin`，音频结束后关闭写入端。
3. Python 完成识别后输出 `final` 事件（完整源文+译文）。
4. Go 汇总结果并写入 `--output` 文本文件。

### 关键事件模型（Python -> Go）

- `status`：阶段状态（启动、request_id、关闭等）。
- `sentence`：一句完整结果（用于控制台展示）。
- `word`：逐词增量结果（用于 TTS 分段缓冲）。
- `final`：最终完整文本（主要用于 transcript 模式落盘）。
- `error`：错误信息（Go 收到后中止流程并返回错误）。

### 编码与中文显示说明

- Go 与 Python 子进程间通信统一使用 UTF-8。
- Go 启动 Python 时设置 `PYTHONUTF8=1`，避免管道场景下 Python 回退到系统代码页（如 GBK）导致中文乱码。
- 不同操作系统终端对 UTF-8 支持不同，建议使用 UTF-8 终端环境运行，确保 `[source]` 中文可正确显示。

## 参数总览

### stream

- `--source-lang` 源语言（必填）
- `--target-lang` 目标语言（必填）
- `--api-key` DashScope API key（可用 `DASHSCOPE_API_KEY`）
- `--sample-rate` 采样率，默认 `16000`
- `--mic-device` 麦克风设备名/编号（Windows:dshow；Linux:pulse；macOS:avfoundation 音频设备编号），默认 `default`（macOS 默认使用 `0`）
- `--enable-tts` 启用硅基流动 TTS
- `--siliconflow-api-key` 硅基流动 key（可用 `SILICONFLOW_API_KEY`）
- `--tts-voice` TTS 音色，默认 `FunAudioLLM/CosyVoice2-0.5B:alex`
- `--tts-speed` TTS 语速，默认 `1.0`
- `--tts-threshold` 标点触发阈值，默认 `8`
- `--python-bin` Python 可执行文件路径，默认 `python`

### transcript

- `--file` 输入文件（`.pcm/.wav/.mp3`，必填）
- `--output` 输出文本路径（必填）
- `--source-lang` 源语言（必填）
- `--target-lang` 目标语言（必填）
- `--api-key` DashScope API key（可用 `DASHSCOPE_API_KEY`）
- `--python-bin` Python 可执行文件路径，默认 `python`

## 常见报错与排查

- **`ffmpeg not found in PATH`**
  - 说明系统找不到 `ffmpeg`，请安装并确保终端可执行 `ffmpeg -version`。
- **`ffplay not found in PATH`**
  - 仅在启用 TTS 时需要，确保终端可执行 `ffplay -version`。
- **stream 无声音频输入**
  - 先按系统枚举设备（Windows: `dshow`；Linux: `pactl`；macOS: `avfoundation`），再用 `--mic-device` 指定正确设备名/编号。
- **`api key required`**
  - 设置 `DASHSCOPE_API_KEY`（和启用 TTS 时的 `SILICONFLOW_API_KEY`）或通过命令参数传入。


## 说明

- Go 负责 CLI、参数、输出文件与流程编排。
- Go 负责 `.pcm/.wav/.mp3` 输入处理、ffmpeg 解码、stream 麦克风采集、TTS 播放。
- Python 网关仅负责 DashScope 实时语音 API 调用与结果回传。

