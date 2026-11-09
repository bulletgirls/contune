#!/usr/bin/env python3
"""docs/cards/*.md를 카드마다 한 파일(specs/<ID>.md)로 나눈다 (표준 라이브러리만).

- 카드 경계: `<a id="xx"></a>` 앵커. 그 앞(파일 제목 아래)의 글은 묶음의 공통 근거로 각 카드에 붙인다.
- 카드 머리 표의 "| 세션 · 시각 |" 행은 뺀다(해커톤 당일 참고값).
- 머리에 pipeline_plan.md의 등급·우선 세션·선행을 적는다.
- docs/cards/README.md는 specs/_COMMON.md가 된다.
다시 돌려도 같은 결과가 나온다. specs/는 직접 고치지 않는다.
"""
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CARDS = os.path.join(ROOT, "docs", "cards")
SPECS = os.path.join(ROOT, "specs")
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import dag  # noqa: E402

ANCHOR = re.compile(r'^<a id="([a-z0-9]+)"></a>\s*$', re.M)
NOTE = "> 이 파일은 `pipeline/split_cards.py`가 `docs/cards/%s`에서 만든 사본이다. 고치려면 원본을 고치고 다시 만든다.\n"


def clean(text):
    lines = [l for l in text.splitlines() if not l.startswith("| 세션 · 시각")]
    text = "\n".join(lines).strip()
    text = re.sub(r"\n-{3,}\s*$", "", text).strip()
    return text


def main():
    plan, order = dag.load_plan()
    os.makedirs(SPECS, exist_ok=True)
    written = set()
    for name in sorted(os.listdir(CARDS)):
        if not name.endswith(".md") or name == "README.md":
            continue
        src = open(os.path.join(CARDS, name), encoding="utf-8").read()
        parts = ANCHOR.split(src)
        head = parts[0]
        title_m = re.search(r"^# (.+)$", head, re.M)
        group_title = title_m.group(1).strip() if title_m else name
        preamble = clean(re.sub(r"^# .+$", "", head, count=1, flags=re.M))
        for i in range(1, len(parts), 2):
            cid = parts[i].upper()
            body = clean(parts[i + 1])
            meta = plan.get(cid)
            if meta:
                info = "> 등급 **%s**(%s) · 우선 세션 %s · 선행 %s\n" % (
                    meta["grade"], dag.MODEL[meta["grade"]], meta["session"],
                    ", ".join(meta["preds"]) if meta["preds"] and len(meta["preds"]) < 20 else ("나머지 전부" if meta["preds"] else "없음"))
            else:
                info = "> (pipeline_plan.md에 없는 카드)\n"
            out = ["# 카드 %s\n" % cid,
                   "> 먼저 `specs/_COMMON.md`(워커 공통 지시문)를 읽는다. 보충 계약 `docs/05-supplement.md`가 02보다 우선한다.\n",
                   info, NOTE % name, "",
                   "## 묶음 공통: %s\n" % group_title, preamble, "", body, ""]
            with open(os.path.join(SPECS, cid + ".md"), "w", encoding="utf-8") as f:
                f.write("\n".join(out))
            written.add(cid)
    common = open(os.path.join(CARDS, "README.md"), encoding="utf-8").read()
    with open(os.path.join(SPECS, "_COMMON.md"), "w", encoding="utf-8") as f:
        f.write(NOTE % "README.md" + "\n" + common)
    missing = [c for c in order if c not in written]
    extra = sorted(written - set(order))
    print("specs: %d장 + _COMMON.md" % len(written))
    if missing:
        print("계획에 있는데 카드가 없음: " + ",".join(missing))
    if extra:
        print("카드가 있는데 계획에 없음: " + ",".join(extra))
    if missing or extra:
        sys.exit(1)


if __name__ == "__main__":
    main()
