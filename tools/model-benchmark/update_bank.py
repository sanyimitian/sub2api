"""Import data from allowlisted official datasets; never update executable scorers."""
from __future__ import annotations

import argparse
import hashlib
import json
import re
import sqlite3
import tempfile
import urllib.request
import zlib
from datetime import datetime, timezone
from pathlib import Path

from prepare import DATASETS, REPOS, questions
from store import Bank

MAX_DOWNLOAD = 1536 * 1024 * 1024


def download(url, path, limit=MAX_DOWNLOAD):
    digest = hashlib.sha256()
    size = 0
    with urllib.request.urlopen(url, timeout=90) as response, path.open("wb") as target:
        while block := response.read(1024 * 1024):
            size += len(block)
            if size > limit:
                raise ValueError("Dataset exceeds download limit")
            target.write(block)
            digest.update(block)
    return digest.hexdigest()


def resolve_datasets():
    resolved = {}
    for category, key in (("coding", "lcb-v6.jsonl"), ("math", "math.parquet"), ("reasoning", "reasoning.parquet")):
        repo = DATASETS[key][0]
        with urllib.request.urlopen("https://huggingface.co/api/datasets/" + repo, timeout=30) as response:
            raw = response.read(2 * 1024 * 1024 + 1)
        if len(raw) > 2 * 1024 * 1024:
            raise ValueError("Dataset metadata exceeds limit")
        data = json.loads(raw)
        revision = data.get("sha", "")
        if not re.fullmatch(r"[a-f0-9]{40}", revision):
            raise ValueError("Dataset revision is invalid")
        names = [item.get("rfilename", "") for item in data.get("siblings", [])]
        if category == "coding":
            files = sorted({name for name in names if re.fullmatch(r"test(?:[1-9][0-9]?)?\.jsonl", name)},
                           key=lambda name: int(re.search(r"\d+", name)[0]) if re.search(r"\d+", name) else 1)
        else:
            files = ["data/test-00000-of-00001.parquet"] if "data/test-00000-of-00001.parquet" in names else []
        if not files or len(files) > 24:
            raise ValueError("Dataset layout is not supported")
        resolved[category] = {"repo": repo, "revision": revision, "files": files}
    return resolved


def validate_question(question):
    for key, maximum in (("id", 200), ("title", 500), ("prompt", 256000)):
        value = question.get(key)
        if not isinstance(value, str) or not value or len(value) > maximum:
            raise ValueError("Invalid question text")
    if question["category"] == "coding":
        if question["difficulty"] not in ("medium", "hard"):
            raise ValueError("Unsupported difficulty")
        sample = json.loads(question["evaluation_sample"]["input_output"])
        inputs, outputs = sample.get("inputs"), sample.get("outputs")
        if not isinstance(inputs, list) or not isinstance(outputs, list) or not 1 <= len(inputs) == len(outputs) <= 10000:
            raise ValueError("Invalid grading samples")
        if not all(isinstance(v, str) for v in inputs + outputs):
            raise ValueError("Invalid grading samples")
        if sample.get("fn_name") is not None and not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", sample["fn_name"]):
            raise ValueError("Unsupported function name")
    elif question["category"] == "math":
        if not re.fullmatch(r"[0-9]{1,3}", str(question.get("ground_truth", ""))):
            raise ValueError("Unsupported AIME answer")
    elif question["category"] != "reasoning" or question.get("task") not in ("zebra_puzzle", "spatial"):
        raise ValueError("Unsupported scoring task")
    raw = json.dumps(question, sort_keys=True, ensure_ascii=False).encode()
    if len(raw) > 16 * 1024 * 1024:
        raise ValueError("Question exceeds limit")
    return raw


def build_bank(home, staging, resolved, hashes):
    partial = staging / "bank.sqlite3"
    with sqlite3.connect(partial) as db:
        db.executescript("CREATE TABLE questions(id TEXT PRIMARY KEY,category TEXT,difficulty TEXT,title TEXT,payload BLOB); CREATE TABLE metadata(key TEXT PRIMARY KEY,value TEXT);")
        skipped = {"oversized": 0, "incompatible": 0}
        for index, question in enumerate(questions(home, datasets_home=staging, coding_files=resolved["coding"]["files"],
                                                  release=datetime.now(timezone.utc).date().isoformat(), expanded=True, skipped=skipped)):
            if index >= 10000:
                raise ValueError("Too many questions")
            try:
                raw = validate_question(question)
            except ValueError:
                skipped["incompatible"] += 1
                continue
            # Releases may overlap; later releases supply the latest copy of an ID.
            db.execute("INSERT OR REPLACE INTO questions VALUES(?,?,?,?,?)",
                       (question["id"], question["category"], question.get("difficulty", ""), question["title"], zlib.compress(raw)))
        digest = hashlib.sha256()
        counts = {key: 0 for key in ("coding", "math", "reasoning")}
        for category, payload in db.execute("SELECT category,payload FROM questions ORDER BY id"):
            digest.update(zlib.decompress(payload)); counts[category] += 1
        if any(counts[k] < n for k, n in {"coding": 20, "math": 5, "reasoning": 5}.items()):
            raise ValueError("Candidate bank is incomplete")
        manifest = {"version": "public-v2-" + digest.hexdigest(), "repos": REPOS, "datasets": resolved,
                    "sha256": hashes, "counts": counts, "skipped": skipped,
                    "import_policy": "official-compatible-v2", "as_of": datetime.now(timezone.utc).date().isoformat(),
                    "scoring": "lcb-stdin-float-fix-v1/livebench-pinned"}
        db.execute("INSERT INTO metadata VALUES('manifest',?)", (json.dumps(manifest),))
    Bank(partial)  # Verify that the persisted bank can be loaded.
    banks = home / "banks"
    banks.mkdir(exist_ok=True, mode=0o700)
    target = banks / (manifest["version"] + ".sqlite3")
    if not target.exists():
        partial.replace(target)
        target.chmod(0o600)
    return manifest["version"]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--home", type=Path, required=True)
    args = parser.parse_args()
    resolved = resolve_datasets()
    # Repeated checks of the same official revisions reuse a verified archive.
    for path in sorted((args.home / "banks").glob("public-v2-*.sqlite3")):
        bank = Bank(path)
        if (bank.manifest.get("datasets") == resolved and bank.manifest.get("import_policy") == "official-compatible-v2"
                and bank.manifest.get("as_of") == datetime.now(timezone.utc).date().isoformat()):
            print(json.dumps({"version": bank.manifest["version"]}))
            return
    with tempfile.TemporaryDirectory(prefix="bank-update-", dir=args.home) as temp:
        staging = Path(temp)
        hashes = {}
        total_size = 0
        for category, source in resolved.items():
            for file in source["files"]:
                filename = file if category == "coding" else category + ".parquet"
                hashes[category + "/" + file] = download(
                    f'https://huggingface.co/datasets/{source["repo"]}/resolve/{source["revision"]}/{file}', staging / filename)
                total_size += (staging / filename).stat().st_size
                if total_size > 6 * 1024 * 1024 * 1024:
                    raise ValueError("Dataset collection exceeds download limit")
        version = build_bank(args.home, staging, resolved, hashes)
    print(json.dumps({"version": version}))


if __name__ == "__main__":
    main()
