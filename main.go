package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

type gatewayEvent struct {
	Type        string `json:"type"`
	Source      string `json:"source,omitempty"`
	Target      string `json:"target,omitempty"`
	Error       string `json:"error,omitempty"`
	Message     string `json:"message,omitempty"`
	SentenceEnd bool   `json:"sentence_end,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "stream":
		if err := runStream(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "transcript":
		if err := runTranscript(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Println("mini-tmk-agent stream --source-lang zh --target-lang en")
	fmt.Println("mini-tmk-agent transcript --file <audio-file> --output <file> --source-lang zh --target-lang en")
}

func runStream(args []string) error {
	fs := flag.NewFlagSet("stream", flag.ContinueOnError)
	sourceLang := fs.String("source-lang", "", "source language")
	targetLang := fs.String("target-lang", "", "target language")
	apiKey := fs.String("api-key", os.Getenv("DASHSCOPE_API_KEY"), "DashScope API key")
	sampleRate := fs.Int("sample-rate", 16000, "audio sample rate")
	micDevice := fs.String("mic-device", "default", "ffmpeg microphone device name")
	enableTTS := fs.Bool("enable-tts", false, "enable SiliconFlow TTS")
	siliconflowKey := fs.String("siliconflow-api-key", os.Getenv("SILICONFLOW_API_KEY"), "SiliconFlow API key")
	ttsVoice := fs.String("tts-voice", "FunAudioLLM/CosyVoice2-0.5B:alex", "SiliconFlow voice")
	ttsSpeed := fs.Float64("tts-speed", 1.0, "SiliconFlow speech speed")
	ttsThreshold := fs.Int("tts-threshold", 8, "punctuation flush threshold")
	pythonBin := fs.String("python-bin", "python", "python executable path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sourceLang == "" || *targetLang == "" {
		return errors.New("source-lang and target-lang are required")
	}
	if *apiKey == "" {
		return errors.New("api key required via --api-key or DASHSCOPE_API_KEY")
	}
	if *enableTTS && *siliconflowKey == "" {
		return errors.New("siliconflow api key required via --siliconflow-api-key or SILICONFLOW_API_KEY")
	}
	gw, err := gatewayPath()
	if err != nil {
		return err
	}
	pyArgs := []string{
		gw,
		"stream",
		"--source-lang", *sourceLang,
		"--target-lang", *targetLang,
		"--api-key", *apiKey,
		"--sample-rate", fmt.Sprintf("%d", *sampleRate),
	}
	pyCmd := exec.Command(*pythonBin, pyArgs...)
	pyCmd.Env = append(os.Environ(), "PYTHONUTF8=1")
	pyStdin, err := pyCmd.StdinPipe()
	if err != nil {
		return err
	}
	pyStdout, err := pyCmd.StdoutPipe()
	if err != nil {
		_ = pyStdin.Close()
		return err
	}
	pyCmd.Stderr = os.Stderr
	if err := pyCmd.Start(); err != nil {
		return err
	}

	var ttsCh chan gatewayEvent
	if *enableTTS {
		ttsCh = make(chan gatewayEvent, 128)
		fmt.Println("[tts] enabled")
		go ttsWorker(ttsCh, *siliconflowKey, *ttsVoice, *ttsSpeed, *ttsThreshold)
	}
	evtErrCh := make(chan error, 1)
	go func() {
		evtErrCh <- handleGatewayEvents(pyStdout, ttsCh, true)
	}()

	ffmpeg, err := findFFmpeg()
	if err != nil {
		_ = pyStdin.Close()
		_ = pyCmd.Wait()
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ffArgs, err := buildStreamInputArgs(*micDevice, *sampleRate)
	if err != nil {
		_ = pyStdin.Close()
		_ = pyCmd.Wait()
		return err
	}
	ffCmd := exec.CommandContext(ctx, ffmpeg, ffArgs...)
	ffOut, err := ffCmd.StdoutPipe()
	if err != nil {
		_ = pyStdin.Close()
		_ = pyCmd.Wait()
		return err
	}
	ffCmd.Stderr = os.Stderr
	if err := ffCmd.Start(); err != nil {
		_ = pyStdin.Close()
		_ = pyCmd.Wait()
		return err
	}

	_, copyErr := io.Copy(pyStdin, ffOut)
	_ = pyStdin.Close()
	ffErr := ffCmd.Wait()
	pyErr := pyCmd.Wait()
	if ttsCh != nil {
		close(ttsCh)
	}
	evtErr := <-evtErrCh
	if evtErr != nil {
		return evtErr
	}
	if copyErr != nil && !errors.Is(copyErr, os.ErrClosed) {
		return copyErr
	}
	if ffErr != nil && ctx.Err() == nil {
		return ffErr
	}
	if pyErr != nil && ctx.Err() == nil {
		return pyErr
	}
	return nil
}

func buildStreamInputArgs(micDevice string, sampleRate int) ([]string, error) {
	device := strings.TrimSpace(micDevice)
	if device == "" {
		device = "default"
	}
	switch runtime.GOOS {
	case "windows":
		return []string{
			"-v", "error",
			"-f", "dshow",
			"-i", "audio=" + device,
			"-f", "s16le",
			"-ac", "1",
			"-ar", fmt.Sprintf("%d", sampleRate),
			"pipe:1",
		}, nil
	case "linux":
		return []string{
			"-v", "error",
			"-use_wallclock_as_timestamps", "1",
			"-fflags", "+genpts",
			"-f", "pulse",
			"-i", device,
			"-f", "s16le",
			"-ac", "1",
			"-ar", fmt.Sprintf("%d", sampleRate),
			"pipe:1",
		}, nil
	case "darwin":
		// avfoundation audio input format uses "<video_device>:<audio_device>".
		if device == "default" {
			device = "0"
		}
		return []string{
			"-v", "error",
			"-f", "avfoundation",
			"-i", ":" + device,
			"-f", "s16le",
			"-ac", "1",
			"-ar", fmt.Sprintf("%d", sampleRate),
			"pipe:1",
		}, nil
	default:
		return nil, fmt.Errorf("unsupported platform for stream capture: %s", runtime.GOOS)
	}
}

func runTranscript(args []string) error {
	fs := flag.NewFlagSet("transcript", flag.ContinueOnError)
	sourceLang := fs.String("source-lang", "", "source language")
	targetLang := fs.String("target-lang", "", "target language")
	file := fs.String("file", "", "input file")
	output := fs.String("output", "", "output text file")
	apiKey := fs.String("api-key", os.Getenv("DASHSCOPE_API_KEY"), "DashScope API key")
	pythonBin := fs.String("python-bin", "python", "python executable path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sourceLang == "" || *targetLang == "" || *file == "" || *output == "" {
		return errors.New("file/output/source-lang/target-lang are required")
	}
	if *apiKey == "" {
		return errors.New("api key required via --api-key or DASHSCOPE_API_KEY")
	}

	gw, err := gatewayPath()
	if err != nil {
		return err
	}
	cmd := exec.Command(*pythonBin, gw,
		"transcript",
		"--source-lang", *sourceLang,
		"--target-lang", *targetLang,
		"--api-key", *apiKey,
		"--sample-rate", "16000",
	)
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := streamAudioAsPCM(*file, stdin); err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		return err
	}
	_ = stdin.Close()

	allSource, allTarget, evtErr := collectTranscriptEvents(stdout)
	if err := cmd.Wait(); err != nil {
		return err
	}
	if evtErr != nil {
		return evtErr
	}

	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf("[source_lang:%s]\n%s\n\n[target_lang:%s]\n%s\n", *sourceLang, allSource, *targetLang, allTarget)
	if err := os.WriteFile(*output, []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Println("[agent] transcript written:", *output)
	return nil
}

func streamAudioAsPCM(file string, w io.Writer) error {
	ext := strings.ToLower(filepath.Ext(file))
	switch ext {
	case ".pcm":
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	case ".wav", ".mp3":
		ffmpeg, err := findFFmpeg()
		if err != nil {
			return err
		}
		cmd := exec.Command(ffmpeg, "-v", "error", "-i", file, "-f", "s16le", "-ac", "1", "-ar", "16000", "pipe:1")
		out, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			return err
		}
		_, cpErr := io.Copy(w, out)
		waitErr := cmd.Wait()
		if cpErr != nil {
			return cpErr
		}
		return waitErr
	default:
		return fmt.Errorf("unsupported input format: %s", ext)
	}
}

func findFFmpeg() (string, error) {
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p, nil
	}
	return "", errors.New("ffmpeg not found in PATH")
}

func gatewayPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	root := filepath.Dir(exe)
	candidates := []string{
		filepath.Join(root, "python_gateway", "gateway.py"),
		filepath.Join(".", "python_gateway", "gateway.py"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("python gateway not found: python_gateway/gateway.py")
}

func collectTranscriptEvents(r io.Reader) (string, string, error) {
	var allSource, allTarget string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		var evt gatewayEvent
		if err := json.Unmarshal(line, &evt); err != nil {
			fmt.Println(string(line))
			continue
		}
		switch evt.Type {
		case "status":
			fmt.Println("[agent]", evt.Message)
		case "sentence":
			fmt.Printf("[source] %s\n", evt.Source)
			fmt.Printf("[target] %s\n", evt.Target)
		case "final":
			allSource = evt.Source
			allTarget = evt.Target
		case "error":
			return "", "", errors.New(evt.Error)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", err
	}
	return allSource, allTarget, nil
}

func handleGatewayEvents(r io.Reader, ttsCh chan<- gatewayEvent, printSentence bool) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		var evt gatewayEvent
		if err := json.Unmarshal(line, &evt); err != nil {
			fmt.Println(string(line))
			continue
		}
		switch evt.Type {
		case "status":
			fmt.Println("[agent]", evt.Message)
		case "sentence":
			if printSentence {
				fmt.Printf("[source] %s\n", evt.Source)
				fmt.Printf("[target] %s\n", evt.Target)
			}
		case "word":
			if ttsCh != nil {
				ttsCh <- evt
			}
		case "error":
			return errors.New(evt.Error)
		}
	}
	return scanner.Err()
}

func ttsWorker(ch <-chan gatewayEvent, apiKey, voice string, speed float64, threshold int) {
	client := &http.Client{Timeout: 60 * time.Second}
	punct := "，。、,.!?！？"
	buffer := ""
	for evt := range ch {
		if evt.Type != "word" {
			continue
		}
		buffer += evt.Target
		flush := evt.SentenceEnd
		if !flush && evt.Target != "" {
			last := []rune(evt.Target)
			c := string(last[len(last)-1])
			if strings.Contains(punct, c) && len([]rune(buffer)) > threshold {
				flush = true
			}
		}
		if !flush || strings.TrimSpace(buffer) == "" {
			continue
		}
		if err := speakSiliconflow(client, apiKey, voice, speed, buffer); err != nil {
			fmt.Println("[tts] error:", err)
		}
		buffer = ""
	}
}

func speakSiliconflow(client *http.Client, apiKey, voice string, speed float64, text string) error {
	payload := fmt.Sprintf(`{"model":"FunAudioLLM/CosyVoice2-0.5B","input":%q,"voice":%q,"response_format":"mp3","stream":true,"speed":%v,"gain":0}`, text, voice, speed)
	req, err := http.NewRequest(http.MethodPost, "https://api.siliconflow.cn/v1/audio/speech", bytes.NewBufferString(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("tts request failed: %s %s", resp.Status, string(b))
	}
	ffplay, err := findFFplay()
	if err != nil {
		return err
	}
	cmd := exec.Command(ffplay, "-nodisp", "-autoexit", "-loglevel", "error", "-i", "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	_, cpErr := io.Copy(in, resp.Body)
	_ = in.Close()
	waitErr := cmd.Wait()
	if cpErr != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return fmt.Errorf("%w; ffplay: %s", cpErr, errMsg)
		}
		return cpErr
	}
	if waitErr != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return fmt.Errorf("%w; ffplay: %s", waitErr, errMsg)
		}
		return waitErr
	}
	return nil
}

func findFFplay() (string, error) {
	if p, err := exec.LookPath("ffplay"); err == nil {
		return p, nil
	}
	return "", errors.New("ffplay not found in PATH")
}
