# mini-tmk-agent-go

Go 主工程 + Python DashScope 网关版本（Windows）。

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
$env:DASHSCOPE_API_KEY="sk-a6d7fa643aa147bf913aa24b9e9f8653"
```

如需 TTS：

```powershell
$env:SILICONFLOW_API_KEY="sk-aookzfowlknrzwrkqxthjwvpmfxlmonnmjrfmlcvypwecfcw"
```

## 构建

```bash
go build -o mini-tmk-agent.exe .
```

## 支持语言

当前参数校验支持：`zh/en/es/ja`

## 流式模式（先取设备名，再运行）

先列出麦克风设备（必做）：

```powershell
ffmpeg -list_devices true -f dshow -i dummy
```

从输出里找到你的音频设备名，比如：

- `麦克风阵列 (适用于数字麦克风的英特尔® 智音技术)`

然后把设备名填入 `--mic-device`：

```bash
.\mini-tmk-agent.exe stream --source-lang zh --target-lang en --mic-device "麦克风阵列 (适用于数字麦克风的英特尔® 智音技术)"
```

启用 TTS（分段播报能力）：

```bash
.\mini-tmk-agent.exe stream --source-lang zh --target-lang en --mic-device "麦克风阵列 (适用于数字麦克风的英特尔® 智音技术)" --enable-tts --tts-speed 1.0 --tts-threshold 8
```

## Transcript 模式

```bash
.\mini-tmk-agent.exe transcript --file D:\go\workspace\mini-tmk-agent\mini-tmk-agent-go\test_data\中文.pcm --output .\out.txt --source-lang zh --target-lang en

.\mini-tmk-agent.exe transcript --file D:\go\workspace\mini-tmk-agent\mini-tmk-agent-go\test_data\英语演讲.wav --output .\out.txt --source-lang en --target-lang zh

.\mini-tmk-agent.exe transcript --file D:\go\workspace\mini-tmk-agent\mini-tmk-agent-go\test_data\英语演讲.mp3 --output .\out.txt --source-lang en --target-lang zh
```

## 参数总览

### stream

- `--source-lang` 源语言（必填）
- `--target-lang` 目标语言（必填）
- `--api-key` DashScope API key（可用 `DASHSCOPE_API_KEY`）
- `--sample-rate` 采样率，默认 `16000`
- `--mic-device` 麦克风设备名（Windows dshow，建议必传）
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
  - 先执行 `ffmpeg -list_devices true -f dshow -i dummy`，再用 `--mic-device` 指定正确设备名。
- **`api key required`**
  - 设置 `DASHSCOPE_API_KEY`（和启用 TTS 时的 `SILICONFLOW_API_KEY`）或通过命令参数传入。


## 说明

- Go 负责 CLI、参数、输出文件与流程编排。
- Go 负责 `.pcm/.wav/.mp3` 输入处理、ffmpeg 解码、stream 麦克风采集、TTS 播放。
- Python 网关仅负责 DashScope 实时语音 API 调用与结果回传。

