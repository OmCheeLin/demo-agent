import argparse
import asyncio
import json
import sys
import threading
import time

import dashscope
from dashscope.audio.asr import TranslationRecognizerCallback, TranslationRecognizerRealtime
from dashscope.common.error import InvalidParameter

FRAME_MS = 100
PCM_WIDTH_BYTES = 2


def chunk_size(sample_rate: int) -> int:
    return sample_rate * PCM_WIDTH_BYTES * FRAME_MS // 1000


class Callback(TranslationRecognizerCallback):
    def __init__(self, target_lang: str, stream_print: bool):
        super().__init__()
        self.target_lang = target_lang
        self.stream_print = stream_print
        self.source_ptr = 0
        self.target_ptr = 0
        self.source_buffer = []
        self.target_buffer = []
        self.all_source = []
        self.all_target = []
        self.closed = threading.Event()

    def on_open(self) -> None:
        print(json.dumps({"type": "status", "message": "recognizer opened"}, ensure_ascii=False), flush=True)

    def on_close(self) -> None:
        print(json.dumps({"type": "status", "message": "recognizer closed"}, ensure_ascii=False), flush=True)
        self.closed.set()

    def on_event(self, request_id, transcription_result, translation_result, usage) -> None:
        if transcription_result is not None:
            for i, word in enumerate(transcription_result.words):
                if word.fixed and i >= self.source_ptr:
                    self.source_buffer.append(word.text)
                    self.all_source.append(word.text)
                    self.source_ptr += 1

        if translation_result is not None:
            trans = translation_result.get_translation(self.target_lang)
            if trans is not None:
                for i, word in enumerate(trans.words):
                    if word.fixed and i >= self.target_ptr:
                        self.target_buffer.append(word.text)
                        self.all_target.append(word.text)
                        if self.stream_print:
                            print(
                                json.dumps(
                                    {
                                        "type": "word",
                                        "target": word.text,
                                        "sentence_end": False,
                                    },
                                    ensure_ascii=False,
                                ),
                                flush=True,
                            )
                        self.target_ptr += 1
                if trans.is_sentence_end:
                    source = "".join(self.source_buffer).strip()
                    target = "".join(self.target_buffer).strip()
                    if source or target:
                        print(
                            json.dumps(
                                {"type": "sentence", "source": source, "target": target},
                                ensure_ascii=False,
                            ),
                            flush=True,
                        )
                    if self.stream_print:
                        print(
                            json.dumps(
                                {"type": "word", "target": "", "sentence_end": True},
                                ensure_ascii=False,
                            ),
                            flush=True,
                        )
                    self.source_buffer.clear()
                    self.target_buffer.clear()
                    self.source_ptr = 0
                    self.target_ptr = 0


def build_recognizer(args, callback: Callback) -> TranslationRecognizerRealtime:
    dashscope.api_key = args.api_key
    return TranslationRecognizerRealtime(
        model="gummy-realtime-v1",
        format="pcm",
        sample_rate=args.sample_rate,
        transcription_enabled=True,
        translation_enabled=True,
        translation_target_languages=[args.target_lang],
        semantic_punctuation_enabled=True,
        callback=callback,
    )


def run_transcript(args):
    callback = Callback(args.target_lang, stream_print=False)
    recognizer = build_recognizer(args, callback)
    print(json.dumps({"type": "status", "message": f"transcript mode started target={args.target_lang}"}, ensure_ascii=False), flush=True)
    recognizer.start()
    print(json.dumps({"type": "status", "message": f"request_id={recognizer.get_last_request_id()}"}, ensure_ascii=False), flush=True)
    stopped = False
    try:
        size = chunk_size(args.sample_rate)
        while True:
            data = sys.stdin.buffer.read(size)
            if not data:
                break
            recognizer.send_audio_frame(data)
        recognizer.stop()
        stopped = True
        callback.closed.wait(timeout=5)
    finally:
        if not stopped:
            try:
                recognizer.stop()
            except InvalidParameter:
                pass

    source = "".join(callback.all_source).strip()
    target = "".join(callback.all_target).strip()
    print(json.dumps({"type": "final", "source": source, "target": target}, ensure_ascii=False), flush=True)


def run_stream(args):
    callback = Callback(args.target_lang, stream_print=True)
    recognizer = build_recognizer(args, callback)
    print(json.dumps({"type": "status", "message": f"stream mode started source={args.source_lang} target={args.target_lang}"}, ensure_ascii=False), flush=True)
    recognizer.start()
    print(json.dumps({"type": "status", "message": f"request_id={recognizer.get_last_request_id()}"}, ensure_ascii=False), flush=True)
    try:
        while True:
            data = sys.stdin.buffer.read(chunk_size(args.sample_rate))
            if not data:
                break
            recognizer.send_audio_frame(data)
    except KeyboardInterrupt:
        print(json.dumps({"type": "status", "message": "stopping stream mode ..."}, ensure_ascii=False), flush=True)
    finally:
        try:
            recognizer.stop()
        except Exception:
            pass
        time.sleep(0.2)


def main():
    if hasattr(asyncio, "WindowsSelectorEventLoopPolicy"):
        try:
            asyncio.set_event_loop_policy(asyncio.WindowsSelectorEventLoopPolicy())
        except Exception:
            pass

    parser = argparse.ArgumentParser()
    parser.add_argument("mode", choices=["stream", "transcript"])
    parser.add_argument("--api-key", required=True)
    parser.add_argument("--source-lang", required=True)
    parser.add_argument("--target-lang", required=True)
    parser.add_argument("--sample-rate", type=int, default=16000)
    parser.add_argument("--file", default="")

    args = parser.parse_args()
    if args.mode == "stream":
        run_stream(args)
    else:
        run_transcript(args)


if __name__ == "__main__":
    main()

