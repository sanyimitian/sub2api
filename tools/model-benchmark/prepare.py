"""Freeze a public question bank and its official scorers. Never executes answers."""
from __future__ import annotations

import argparse
import base64
import hashlib
import importlib.util
import io
import json
import os
import pickle
import re
import shutil
import sqlite3
import subprocess
import sys
import urllib.request
import zlib
from pathlib import Path

REPOS = {
    "LiveCodeBench": ("https://github.com/LiveCodeBench/LiveCodeBench.git", "28fef95ea8c9f7a547c8329f2cd3d32b92c1fa24"),
    "LiveBench": ("https://github.com/LiveBench/LiveBench.git", "1d1006a0d10ece52c2e706f8a208a659b5bbb96f"),
}
DATASETS = {
    "lcb-v6.jsonl": ("livecodebench/code_generation_lite", "0fe84c3912ea0c4d4a78037083943e8f0c4dd505", "test6.jsonl"),
    "math.parquet": ("livebench/math", "bb66571c8ccf32d3df9e6f48b920d3770ff4aacb", "data/test-00000-of-00001.parquet"),
    "reasoning.parquet": ("livebench/reasoning", "6fc6498a5dfba553f69f4413feabade1f1a2d384", "data/test-00000-of-00001.parquet"),
}


def float_tolerance(statement):
    criteria = re.findall(r"[^\n.]*\b(?:absolute|relative)\s+error\b[^\n.]*", statement, re.I)
    if not criteria:
        return None
    match = re.search(r"10\s*\^\s*\{?\s*-(\d+)\s*\}?", " ".join(criteria))
    if not match:
        raise ValueError("Unsupported floating-point criterion")
    return 10 ** -int(match[1])


def sources(home, cache, download_datasets=True):
    for name, (url, commit) in REPOS.items():
        target = home / "sources" / name
        target.parent.mkdir(parents=True, exist_ok=True)
        if not target.exists():
            staging = target.with_name(target.name + ".installing")
            if staging.exists():
                shutil.rmtree(staging)
            cached = cache / "sources" / name if cache else None
            if cached and cached.is_dir():
                subprocess.run(["git", "clone", "--no-hardlinks", str(cached), str(staging)], check=True)
            else:
                subprocess.run(["git", "clone", "--filter=blob:none", url, str(staging)], check=True)
            subprocess.run(["git", "checkout", "--detach", commit], cwd=staging, check=True)
            staging.replace(target)
        actual = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=target, text=True).strip()
        dirty = subprocess.check_output(["git", "status", "--porcelain", "--untracked-files=no"], cwd=target, text=True).strip()
        if actual != commit or dirty:
            raise RuntimeError(f"{name} must be clean at {commit}; refusing to modify existing sources")
        if name == "LiveCodeBench":
            make_grader_sources_readable(target)
    if not download_datasets:
        return {}
    datasets = home / "datasets"
    datasets.mkdir(exist_ok=True)
    hashes = {}
    for name, (repo, revision, file) in DATASETS.items():
        target = datasets / name
        if not target.exists():
            partial = target.with_suffix(target.suffix + ".part")
            cached = cache / "datasets" / name if cache else None
            if cached and cached.is_file():
                shutil.copyfile(cached, partial)
            else:
                with urllib.request.urlopen(f"https://huggingface.co/datasets/{repo}/resolve/{revision}/{file}", timeout=120) as response, partial.open("wb") as out:
                    shutil.copyfileobj(response, out)
            partial.replace(target)
        digest = hashlib.sha256()
        with target.open("rb") as source:
            for block in iter(lambda: source.read(1024 * 1024), b""):
                digest.update(block)
        hashes[name] = digest.hexdigest()
    return hashes


def make_grader_sources_readable(source):
    # The worker runs with umask 077; the read-only sandbox uses nobody. Only
    # these pinned public sources are shared, never benchmark data or secrets.
    for directory, subdirectories, files in os.walk(source, followlinks=False):
        current = Path(directory)
        if current.is_symlink():
            continue
        current.chmod(0o755)
        subdirectories[:] = [name for name in subdirectories if name != ".git" and not (current / name).is_symlink()]
        for name in files:
            path = current / name
            if path.is_symlink():
                continue
            mode = path.stat().st_mode
            path.chmod(0o755 if mode & 0o111 else 0o644)


class PrivateTestsTooLarge(ValueError):
    pass


def safe_private_tests(value):
    try:
        return json.dumps(json.loads(value))
    except (ValueError, TypeError):
        class DataOnlyUnpickler(pickle.Unpickler):
            def find_class(self, module, name):
                raise ValueError("Executable objects are not allowed in test data")
            def persistent_load(self, pid):
                raise ValueError("External objects are not allowed in test data")
        compressed = base64.b64decode(value, validate=True)
        decoder = zlib.decompressobj()
        raw = decoder.decompress(compressed, 32 * 1024 * 1024 + 1)
        if len(raw) > 32 * 1024 * 1024 or not decoder.eof:
            raise PrivateTestsTooLarge("Private test data exceeds its limit")
        decoded = DataOnlyUnpickler(io.BytesIO(raw)).load()
        if not isinstance(decoded, str):
            raise ValueError("Unexpected private test data")
        return json.dumps(json.loads(decoded))


def bounded_jsonl(path, skipped):
    with path.open("rb") as source:
        while line := source.readline(16 * 1024 * 1024 + 1):
            if len(line) > 16 * 1024 * 1024:
                while line and not line.endswith(b"\n"):
                    line = source.readline(1024 * 1024)
                skipped["oversized"] += 1
                continue
            yield json.loads(line)


def questions(home, *, datasets_home=None, coding_files=None, release="2024-11-25", expanded=False, skipped=None):
    import pyarrow.parquet as pq

    lcb = home / "sources/LiveCodeBench"
    sys.path.insert(0, str(lcb))
    from lcb_runner.benchmarks.code_generation import CodeGenerationProblem

    # Loading this file directly avoids optional provider SDK imports. The pinned
    # prompt helper loads its few-shot files relative to the source root.
    previous = Path.cwd()
    os.chdir(lcb)
    try:
        spec = importlib.util.spec_from_file_location("benchmark_prompts", lcb / "lcb_runner/prompts/code_generation.py")
        prompts = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(prompts)
    finally:
        os.chdir(previous)
    datasets_home = datasets_home or home / "datasets"
    skipped = skipped if skipped is not None else {"oversized": 0, "incompatible": 0}
    for coding_file in coding_files or ["lcb-v6.jsonl"]:
        for row in bounded_jsonl(datasets_home / coding_file, skipped):
            if row["difficulty"] not in ("medium", "hard"):
                continue
            try:
                tolerance = float_tolerance(row["question_content"])
            except ValueError:
                print("Excluded unsupported numeric tolerance:", row["question_id"])
                skipped["incompatible"] += 1
                continue
            try:
                row["private_test_cases"] = safe_private_tests(row["private_test_cases"])
            except PrivateTestsTooLarge:
                skipped["oversized"] += 1
                continue
            problem = CodeGenerationProblem(**row)
            sample = problem.get_evaluation_sample()
            if not json.loads(sample["input_output"])["inputs"]:
                continue
            yield {"id": f"lcb:{problem.platform.value}:{problem.question_id}", "category": "coding",
                   "difficulty": row["difficulty"], "title": problem.question_title,
                   "prompt": prompts.get_generic_question_template_answer(problem),
                   "evaluation_sample": sample, "float_tolerance": tolerance}
    for category in ("math", "reasoning"):
        for row in pq.read_table(datasets_home / f"{category}.parquet").to_pylist():
            if str(row["livebench_release_date"])[:10] > release:
                continue
            if row["livebench_removal_date"] and str(row["livebench_removal_date"])[:10] <= release:
                continue
            if category == "math" and not (re.fullmatch(r"aime_(?:i|ii)_\d{4}", row.get("subtask") or "") if expanded else row["subtask"] in ("aime_i_2024", "aime_ii_2024")):
                continue
            if category == "reasoning" and row["task"] not in ("zebra_puzzle", "spatial"):
                continue
            yield {"id": f"lb:{category}:{row['question_id']}", "category": category, "difficulty": "",
                   "title": row.get("subtask") or row["task"], "prompt": row["turns"][0],
                   "ground_truth": row["ground_truth"], "task": row["task"],
                   "release": str(row["livebench_release_date"])[:10]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--home", type=Path, default=Path(os.getenv("MODEL_BENCHMARK_HOME", "/opt/sub2api-model-benchmark")))
    parser.add_argument("--cache", type=Path, help="Optional existing pinned datasets/sources directory")
    args = parser.parse_args()
    home = args.home.resolve()
    home.mkdir(parents=True, exist_ok=True)
    target = home / "bank.sqlite3"
    if target.exists():
        sources(home, args.cache, download_datasets=False)
        print("Bank already exists; preserved so historical runs remain reproducible:", target)
        return
    hashes = sources(home, args.cache)
    partial = home / "bank.building.sqlite3"
    partial.unlink(missing_ok=True)
    counts = {"coding": 0, "math": 0, "reasoning": 0}
    digest = hashlib.sha256()
    with sqlite3.connect(partial) as db:
        db.executescript("CREATE TABLE questions(id TEXT PRIMARY KEY, category TEXT, difficulty TEXT, title TEXT, payload BLOB); CREATE TABLE metadata(key TEXT PRIMARY KEY,value TEXT);")
        for question in questions(home):
            raw = json.dumps(question, sort_keys=True, ensure_ascii=False).encode()
            digest.update(raw)
            db.execute("INSERT INTO questions VALUES(?,?,?,?,?)", (question["id"], question["category"], question["difficulty"], question["title"], zlib.compress(raw)))
            counts[question["category"]] += 1
        if any(counts[k] < n for k, n in {"coding": 20, "math": 5, "reasoning": 5}.items()):
            raise RuntimeError("Insufficient questions in pinned datasets")
        manifest = {"version": "public-v1-" + digest.hexdigest(), "repos": REPOS, "datasets": DATASETS,
                    "sha256": hashes, "counts": counts, "scoring": "lcb-stdin-float-fix-v1/livebench-pinned"}
        db.execute("INSERT INTO metadata VALUES('manifest',?)", (json.dumps(manifest),))
    partial.replace(target)
    (home / "manifest.json").write_text(json.dumps(manifest, indent=2))
    print(json.dumps({"bank": str(target), "counts": counts, "version": manifest["version"]}))


if __name__ == "__main__":
    main()
