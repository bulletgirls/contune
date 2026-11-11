# 수집 설정 (05 §17)

매니저가 에이전트에 내려보내는 로그 출처다. `*.yaml` 하나가 범위 하나다.

- 범위는 `all`, `group:<그룹 경로>`, `host:<호스트 id>` 중 하나다.
- 화면 `#collect`에서 고치면 이 폴더의 파일을 다시 쓴다.
- 손으로 고쳤으면 `POST /api/v1/collect/reload`를 부르거나 화면의 [다시 읽기]를 누른다.
- 에이전트는 자기 YAML에 `remote.enabled: true`가 있어야 받는다. `remote.read_roots` 밖의 파일은 읽지 않는다.

```yaml
scope: group:매매/체결
logs:
  - name: matching
    path: "{log_dir}/matching_svc.{exec_id}.log"
    match:
      - { re: '^\(E\)', level: error }
      - { re: '^\(W\)', level: warn }
    service: matching   # 비우면 경로의 {exec_id}가 주인, 있으면 그 서비스 인스턴스(svc:matching@호스트)가 주인(06)
```
