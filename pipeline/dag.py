#!/usr/bin/env python3
"""이음 카드 파이프라인의 상태 관리 (표준 라이브러리만, Python 3.8+).

pipeline_plan.md의 표를 읽고 dag_state.json에 진행 상태를 기록한다.
오케스트레이터는 JSON을 직접 고치지 않고 이 스크립트만 부른다.

  init [--force]       계획에서 dag_state.json을 새로 만든다
  check                계획 검증(선행 존재, 순환, specs 파일)
  resume               이전 세션에서 running으로 남은 카드를 태그로 판정(있으면 done, 없으면 pending)
  launch [--max N]     실행 가능한 카드를 골라 running으로 바꾸고, 서브에이전트 호출 정보를 JSON 배열로 출력
  done <ID>            태그 done-<ID>가 있으면 done (없으면 종료 코드 1)
  fail <ID>            실패 기록. 시도가 MAX_ATTEMPTS 미만이면 pending, 아니면 blocked
  reset <ID>           pending으로 되돌림(시도 수 -1). 서브에이전트를 띄우지 못했을 때
  skip <ID>            넓히기 카드(SKIPPABLE)만: skipped로 두고 뒤 카드가 진행하게 함(병합 대상에서 빠짐)
  status               요약
  finish               모든 카드가 done/skipped이면 main을 done-<마지막 카드>로 빨리 감기

시도 1·2회차는 등급의 모델, 3회차부터는 opus이고 선행 카드 코드까지 고칠 수 있다(지시문에 적힘).
"""
import json
import os
import subprocess
import sys
import time

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PLAN = os.path.join(ROOT, "pipeline_plan.md")
STATE = os.path.join(ROOT, "dag_state.json")
SPECS = os.path.join(ROOT, "specs")
LOGS = os.path.join(ROOT, "pipeline", "logs")
WT_PARENT = os.path.dirname(ROOT)
SESSIONS = ["s1", "s2", "s3", "s4"]
MODEL = {"상": "opus", "하": "sonnet"}
MAX_ATTEMPTS = 4
ESCALATE_AT = 3          # 이 시도부터 opus + 범위 밖 수정 허용
SKIPPABLE = {"B6", "X2"}  # 넓히기 카드: 끝내 실패하면 건너뛸 수 있음
SATISFIED = ("done", "skipped")


def die(msg, code=2):
    print(msg, file=sys.stderr)
    sys.exit(code)


def load_plan():
    cards = {}
    order = []
    with open(PLAN, encoding="utf-8") as f:
        for line in f:
            if not line.startswith("|"):
                continue
            cols = [c.strip() for c in line.strip().strip("|").split("|")]
            if len(cols) < 6 or cols[0] in ("ID", "") or set(cols[0]) <= set("-:"):
                continue
            cid, grade, sess, preds, title, src = cols[:6]
            if grade not in MODEL:
                die("계획 오류: %s 등급 %r" % (cid, grade))
            if sess not in SESSIONS:
                die("계획 오류: %s 세션 %r" % (cid, sess))
            plist = [] if preds in ("-", "") else [p.strip() for p in preds.split(",") if p.strip()]
            cards[cid] = {"id": cid, "grade": grade, "session": sess, "preds": plist,
                          "title": title, "src": src}
            order.append(cid)
    for cid in order:
        if cards[cid]["preds"] == ["ALL"]:
            cards[cid]["preds"] = [c for c in order if c != cid]
    return cards, order


def load_state():
    if not os.path.exists(STATE):
        die("dag_state.json이 없습니다. 먼저 `python3 pipeline/dag.py init`")
    with open(STATE, encoding="utf-8") as f:
        return json.load(f)


def save_state(st):
    tmp = STATE + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(st, f, ensure_ascii=False, indent=1, sort_keys=True)
        f.write("\n")
    os.replace(tmp, STATE)


def now():
    return time.strftime("%Y-%m-%d %H:%M:%S")


def tag_exists(cid):
    r = subprocess.run(["git", "-C", ROOT, "rev-parse", "-q", "--verify", "refs/tags/done-" + cid],
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    return r.returncode == 0


def stash_leftovers(cid, sess, attempt):
    """실패·중단한 카드가 워크트리에 남긴 작업을 그 카드 이름의 stash로 치운다.
    stash는 모든 워크트리가 공유하므로 재시도 워커가 어느 세션에서든 꺼내 쓸 수 있다."""
    if not sess:
        return ""
    wt = os.path.join(WT_PARENT, "wt-" + sess)
    if not os.path.isdir(wt):
        return ""
    git = ["git", "-C", wt]
    if subprocess.run(git + ["rev-parse", "-q", "--verify", "MERGE_HEAD"],
                      stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
        subprocess.run(git + ["merge", "--abort"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    dirty = subprocess.run(git + ["status", "--porcelain"], stdout=subprocess.PIPE,
                           stderr=subprocess.DEVNULL, universal_newlines=True).stdout.strip()
    if not dirty:
        return ""
    name = "fail-%s-a%d" % (cid, attempt)
    r = subprocess.run(git + ["stash", "push", "-u", "-m", name], stdout=subprocess.DEVNULL,
                       stderr=subprocess.PIPE, universal_newlines=True)
    return (" · 남은 작업을 stash '%s'로 치움" % name) if r.returncode == 0 else (" · stash 실패: " + r.stderr.strip()[:200])


def descendants_depth(cards, order):
    """카드마다 '이 카드 뒤로 이어지는 가장 긴 의존 사슬 길이'(우선순위)."""
    children = {c: [] for c in order}
    for c in order:
        for p in cards[c]["preds"]:
            children[p].append(c)
    memo = {}

    def depth(c):
        if c not in memo:
            memo[c] = 1 + max([depth(k) for k in children[c]] or [0])
        return memo[c]

    return {c: depth(c) for c in order}


def cmd_check():
    cards, order = load_plan()
    errs = []
    for c in order:
        for p in cards[c]["preds"]:
            if p not in cards:
                errs.append("%s: 없는 선행 %s" % (c, p))
    # 순환
    state = {}

    def visit(c, path):
        if state.get(c) == 1:
            errs.append("순환: " + " → ".join(path + [c]))
            return
        if state.get(c) == 2:
            return
        state[c] = 1
        for p in cards[c]["preds"]:
            if p in cards:
                visit(p, path + [c])
        state[c] = 2

    for c in order:
        visit(c, [])
    for c in order:
        if not os.path.exists(os.path.join(SPECS, c + ".md")):
            errs.append("%s: specs/%s.md 없음" % (c, c))
    if not os.path.exists(os.path.join(SPECS, "_COMMON.md")):
        errs.append("specs/_COMMON.md 없음")
    if errs:
        print("\n".join(errs))
        sys.exit(1)
    print("OK: 카드 %d장, 상 %d · 하 %d" % (len(order), sum(1 for c in order if cards[c]["grade"] == "상"),
                                        sum(1 for c in order if cards[c]["grade"] == "하")))


def cmd_init(force):
    cards, order = load_plan()
    if os.path.exists(STATE) and not force:
        die("dag_state.json이 이미 있습니다(덮어쓰려면 --force)")
    st = {"created": now(), "sessions": {s: None for s in SESSIONS},
          "cards": {c: {"status": "pending", "session": None, "attempts": 0,
                        "started": None, "ended": None} for c in order}}
    save_state(st)
    print("dag_state.json: 카드 %d장 pending" % len(order))


def sync_cards(st, cards, order):
    """계획에 새로 생긴 카드는 pending으로 더한다."""
    for c in order:
        if c not in st["cards"]:
            st["cards"][c] = {"status": "pending", "session": None, "attempts": 0,
                              "started": None, "ended": None}


def cmd_resume():
    cards, order = load_plan()
    st = load_state()
    sync_cards(st, cards, order)
    out = []
    for c in order:
        cs = st["cards"][c]
        if cs["status"] == "running":
            if tag_exists(c):
                cs["status"], cs["ended"] = "done", now()
                out.append("%s: 태그 있음 → done" % c)
            else:
                cs["status"] = "pending"
                note = stash_leftovers(c, cs["session"], cs["attempts"])
                out.append("%s: 태그 없음 → pending(시도 %d)%s" % (c, cs["attempts"], note))
            cs["session"] = None
        elif cs["status"] != "done" and tag_exists(c):
            cs["status"], cs["ended"] = "done", now()
            out.append("%s: 태그 있음 → done" % c)
    st["sessions"] = {s: None for s in SESSIONS}
    for c in order:
        if st["cards"][c]["status"] == "running":
            st["sessions"][st["cards"][c]["session"]] = c
    save_state(st)
    print("\n".join(out) or "정리할 것 없음")


def prompt_for(card, sess, attempt, st):
    n = sess[1:]
    wt = os.path.join(WT_PARENT, "wt-" + sess)
    preds = [p for p in card["preds"] if st["cards"][p]["status"] == "done"]
    skipped = [p for p in card["preds"] if st["cards"][p]["status"] == "skipped"]
    log = os.path.join(LOGS, card["id"] + ".log")
    lines = [
        "너는 [세션 %s] 전담 워커다. 작업 폴더는 %s 이고 브랜치는 %s다." % (sess, wt, sess),
        "셸의 cd는 다음 호출로 이어지지 않으니 모든 셸 명령을 `cd %s && …`로 시작하고, 파일 도구에는 절대 경로를 써라." % wt,
        "카드: %s %s [%s] · 선행: %s · 시도: %d회차" % (card["id"], card["title"], card["grade"],
                                                  ", ".join(preds) if preds else "없음", attempt),
        "",
        "1. `git status`를 확인한 뒤 선행마다 `git merge --no-edit done-<선행ID>`를 해라"
        + ("(선행: %s)." % " ".join(preds) if preds else "(선행 없음)."),
        "   충돌이 나면 양쪽 카드의 의도를 살려 직접 해결하고 커밋해라. 병합 뒤 go.mod가 있으면 `go build ./... && go test ./...`로 기준선을 기록해라.",
        "2. `specs/_COMMON.md`, `specs/%s.md`, 선행 카드의 `pipeline/reports/<선행ID>.md`를 읽고 카드대로 테스트 먼저 → 구현 → 확인해라." % card["id"],
        "   수동 확인용 프로세스는 127.0.0.1:1808%s(매니저), 1910%s(push), 데이터는 작업 폴더의 `.run/`만 쓰고, 끝나면 반드시 끈다." % (n, n),
        "3. 성공하면 `pipeline/reports/%s.md`(공통 지시문 §4 양식)를 함께 커밋하고 `git tag -f done-%s`를 만들어라." % (card["id"], card["id"]),
        "4. 실패하면 태그를 만들지 말고 %s 에 원인·실패한 명령과 출력 끝 100줄·제안을 써라(공통 지시문 §5)." % log,
    ]
    if skipped:
        lines += ["", "건너뛴 선행 카드(병합하지 않음, 그 기능 없이 진행): %s. 그 카드가 채울 stub은 stub 그대로다." % " ".join(skipped)]
    if attempt > 1:
        lines += ["", "이전 시도가 실패했다. 먼저 %s 를 읽고 그 원인부터 해결해라. 이전 시도의 커밋 안 된 작업은 "
                  "`git stash list`에서 `fail-%s-a<번호>`로 찾을 수 있다. 쓸 만하면 선행 병합 뒤 `git stash apply <그 stash>`로 가져오고, "
                  "아니면 무시한다(다른 카드 이름의 stash는 건드리지 않는다)." % (log, card["id"])]
    if attempt >= ESCALATE_AT:
        lines += ["이번은 %d회차라 범위 제한을 푼다: 원인이 선행 카드의 코드나 계약에 있으면 그곳을 직접 고쳐도 된다"
                  "(가장 작게, 테스트로 보호하고, 보고의 \"계약 변경\"·\"범위 밖 수정\"에 적어라)." % attempt]
    lines += ["", "메인 세션의 토큰 절약을 위해 작업 과정은 설명하지 말고, 마지막 응답은 오직 한 줄만 출력하고 끝내라:",
              "`SUCCESS: %s` 또는 `FAIL: %s %s`" % (card["id"], card["id"], log)]
    return "\n".join(lines)


def cmd_launch(maxn):
    cards, order = load_plan()
    st = load_state()
    sync_cards(st, cards, order)
    prio = descendants_depth(cards, order)
    free = [s for s in SESSIONS if st["sessions"].get(s) is None]
    ready = [c for c in order if st["cards"][c]["status"] == "pending"
             and all(st["cards"][p]["status"] in SATISFIED for p in cards[c]["preds"])]
    # 긴 사슬 앞의 카드, 상 등급, 계획 순서
    ready.sort(key=lambda c: (-prio[c], 0 if cards[c]["grade"] == "상" else 1, order.index(c)))
    out = []
    for c in ready:
        if not free or (maxn and len(out) >= maxn):
            break
        cs = st["cards"][c]
        last, pref = cs.get("last_session"), cards[c]["session"]
        sess = last if last in free else (pref if pref in free else free[0])
        free.remove(sess)
        cs["last_session"] = sess
        cs["attempts"] += 1
        cs["status"], cs["session"], cs["started"] = "running", sess, now()
        st["sessions"][sess] = c
        model = "opus" if cs["attempts"] >= ESCALATE_AT else MODEL[cards[c]["grade"]]
        out.append({"id": c, "session": sess, "model": model,
                    "subagent_type": "general-purpose",
                    "description": "%s 카드 (%s)" % (c, sess),
                    "prompt": prompt_for(cards[c], sess, cs["attempts"], st)})
    save_state(st)
    print(json.dumps(out, ensure_ascii=False, indent=1))


def finish_card(cid, status):
    cards, order = load_plan()
    st = load_state()
    if cid not in st["cards"]:
        die("모르는 카드 " + cid)
    cs = st["cards"][cid]
    if cs["session"]:
        st["sessions"][cs["session"]] = None
    cs["session"], cs["ended"] = None, now()
    cs["status"] = status
    save_state(st)
    return cs


def cmd_done(cid):
    if not tag_exists(cid):
        print("태그 done-%s 없음 → fail로 처리하세요" % cid)
        sys.exit(1)
    finish_card(cid, "done")
    print("%s done" % cid)


def cmd_fail(cid):
    st = load_state()
    cs = st["cards"][cid]
    attempts = cs["attempts"]
    note = stash_leftovers(cid, cs["session"], attempts)
    status = "pending" if attempts < MAX_ATTEMPTS else "blocked"
    finish_card(cid, status)
    print("%s → %s (시도 %d/%d)%s" % (cid, status, attempts, MAX_ATTEMPTS, note))


def cmd_reset(cid):
    st = load_state()
    cs = st["cards"][cid]
    if cs["session"]:
        st["sessions"][cs["session"]] = None
    cs["status"], cs["session"] = "pending", None
    cs["attempts"] = max(0, cs["attempts"] - 1)
    save_state(st)
    print("%s → pending" % cid)


def cmd_skip(cid):
    if cid not in SKIPPABLE:
        die("%s는 건너뛸 수 없는 카드입니다(넓히기 카드만: %s)" % (cid, ",".join(sorted(SKIPPABLE))), 1)
    finish_card(cid, "skipped")
    print("%s skipped" % cid)


def cmd_status():
    cards, order = load_plan()
    st = load_state()
    sync_cards(st, cards, order)
    by = {}
    for c in order:
        by.setdefault(st["cards"][c]["status"], []).append(c)
    total = len(order)
    print("done %d/%d · skipped %s · running %s · pending %d · blocked %s" % (
        len(by.get("done", [])), total, ",".join(by.get("skipped", [])) or "-",
        ",".join("%s@%s" % (c, st["cards"][c]["session"]) for c in by.get("running", [])) or "-",
        len(by.get("pending", [])), ",".join(by.get("blocked", [])) or "-"))
    # 막힌 카드 때문에 영영 못 도는 카드
    blocked = set(by.get("blocked", []))
    if blocked:
        stuck = [c for c in order if st["cards"][c]["status"] == "pending"
                 and any(p in blocked for p in cards[c]["preds"])]
        print("막힌 카드에 걸린 pending: " + (",".join(stuck) or "-"))
    if len(by.get("done", [])) + len(by.get("skipped", [])) == total:
        print("ALL_DONE")


def cmd_finish():
    cards, order = load_plan()
    st = load_state()
    left = [c for c in order if st["cards"][c]["status"] not in SATISFIED]
    if left:
        die("아직 끝나지 않은 카드: " + ",".join(left), 1)
    last = order[-1]
    r = subprocess.run(["git", "-C", ROOT, "merge", "--ff-only", "done-" + last])
    if r.returncode != 0:
        r = subprocess.run(["git", "-C", ROOT, "merge", "--no-edit", "done-" + last])
        if r.returncode != 0:
            die("main 병합 실패. 수동 확인이 필요합니다", 1)
    print("main ← done-%s" % last)


def main():
    a = sys.argv[1:]
    if not a:
        print(__doc__)
        return
    c = a[0]
    if c == "init":
        cmd_init("--force" in a)
    elif c == "check":
        cmd_check()
    elif c == "resume":
        cmd_resume()
    elif c == "launch":
        n = 0
        if "--max" in a:
            n = int(a[a.index("--max") + 1])
        cmd_launch(n)
    elif c in ("done", "fail", "reset", "skip") and len(a) == 2:
        {"done": cmd_done, "fail": cmd_fail, "reset": cmd_reset, "skip": cmd_skip}[c](a[1])
    elif c == "status":
        cmd_status()
    elif c == "finish":
        cmd_finish()
    else:
        die(__doc__)


if __name__ == "__main__":
    main()
