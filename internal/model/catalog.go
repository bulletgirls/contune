package model

// MetricDef는 카탈로그의 지표 항목이다.
//   - Title은 차트 안 계열 이름이다. 차트 제목이 대상을 말하므로 짧게 쓴다(차트 "디스크 처리량"의 "읽기").
//   - Chart는 그릴 차트(같은 그룹의 ChartDef.ID)다. 한 차트의 항목은 단위가 같아야 한다.
//   - Derived는 다른 항목에서 계산되는 값이다(예: CPU 사용률 = 사용자 + 시스템). 선을 긋지 않고
//     차트 범례 표의 아래 줄과 커서 말풍선에만 나온다.
type MetricDef struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Unit    string `json:"unit"`
	Chart   string `json:"chart"`
	Procfs  bool   `json:"procfs"` // procfs 수집기가 내는가
	Derived bool   `json:"derived,omitempty"`
	Desc    string `json:"desc,omitempty"` // ⓘ 말풍선: 정의와 출처
}

// ChartDef는 서브 탭 안 차트 하나다.
//   - Stack: 항목을 쌓은 영역으로 그린다(구성비. 맨 위가 합계).
//   - Max: y축 위 끝을 고정한다(% 사용률은 100). 0이면 데이터에 맞춘다. 아래 끝은 늘 0.
//   - Overview: 개요 탭에도 그린다.
//   - Scope "job": 서버 전체가 아니라 실행 하나(exec_id)의 차트다. 프로세스 탭에서 실행을 고르면 그린다.
type ChartDef struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Stack    bool    `json:"stack,omitempty"`
	Max      float64 `json:"max,omitempty"`
	Overview bool    `json:"overview,omitempty"`
	Scope    string  `json:"scope,omitempty"`
}

// MetricGroup은 서버 상세의 서브 탭 하나다. Key는 탭 카드에 띄울 대표 값(여러 개면 합)이다.
type MetricGroup struct {
	ID      string      `json:"id"`
	Title   string      `json:"title"`
	Key     []string    `json:"key"`
	Charts  []ChartDef  `json:"charts"`
	Metrics []MetricDef `json:"metrics"`
}

// Catalog는 서버 상세의 서브 탭·차트·항목이다(05 §2·§3). 항목을 늘릴 때는 Metrics에 한 줄을 더하고
// 새 차트가 필요하면 Charts에도 한 줄을 더한다. 화면 코드는 바뀌지 않는다.
func Catalog() []MetricGroup {
	return []MetricGroup{
		{ID: "cpu", Title: "CPU", Key: []string{"host.cpu.pct"},
			Charts: []ChartDef{
				{ID: "cpu_use", Title: "CPU 사용률", Stack: true, Max: 100, Overview: true},
				{ID: "cpu_load", Title: "부하 평균"},
				{ID: "cpu_runq", Title: "실행 큐"},
				{ID: "cpu_intr", Title: "인터럽트"},
				{ID: "cpu_ctxsw", Title: "컨텍스트 스위치"},
				{ID: "cpu_syscall", Title: "시스템 콜"},
			},
			Metrics: []MetricDef{
				{Name: "host.cpu.user_pct", Title: "사용자", Unit: "%", Chart: "cpu_use", Procfs: true, Desc: "사용자 모드 시간(user+nice)"},
				{Name: "host.cpu.sys_pct", Title: "시스템", Unit: "%", Chart: "cpu_use", Procfs: true, Desc: "커널 모드 시간(system+irq+softirq)"},
				{Name: "host.cpu.wait_pct", Title: "I/O 대기", Unit: "%", Chart: "cpu_use", Procfs: true, Desc: "디스크 I/O를 기다리며 쉰 시간(iowait)"},
				{Name: "host.cpu.steal_pct", Title: "스틸", Unit: "%", Chart: "cpu_use", Procfs: true, Desc: "가상 머신에서 하이퍼바이저가 가져간 시간(steal)"},
				{Name: "host.cpu.pct", Title: "CPU 사용률", Unit: "%", Chart: "cpu_use", Procfs: true, Derived: true, Desc: "사용자 + 시스템"},
				{Name: "host.cpu.busy_wait_pct", Title: "사용률+I/O 대기", Unit: "%", Chart: "cpu_use", Procfs: true, Derived: true, Desc: "사용자 + 시스템 + I/O 대기"},
				{Name: "host.cpu.idle_pct", Title: "유휴", Unit: "%", Chart: "cpu_use", Procfs: true, Derived: true, Desc: "100 − 나머지"},
				{Name: "host.load1", Title: "1분", Unit: "", Chart: "cpu_load", Procfs: true, Desc: "1분 부하 평균(/proc/loadavg). 코어 수보다 크면 CPU를 기다리는 작업이 있음"},
				{Name: "host.proc.running", Title: "실행 큐", Unit: "개", Chart: "cpu_runq", Procfs: true, Desc: "실행 중이거나 CPU를 기다리는 프로세스 수(vmstat r, /proc/stat procs_running)"},
				{Name: "host.sys.intr_ps", Title: "인터럽트", Unit: "회/초", Chart: "cpu_intr", Procfs: true},
				{Name: "host.sys.ctxsw_ps", Title: "컨텍스트 스위치", Unit: "회/초", Chart: "cpu_ctxsw", Procfs: true},
				{Name: "host.sys.syscall_ps", Title: "시스템 콜", Unit: "회/초", Chart: "cpu_syscall", Procfs: false, Desc: "procfs로는 셀 수 없음(확장: eBPF)"},
			}},
		{ID: "mem", Title: "메모리", Key: []string{"host.mem.used_pct"},
			Charts: []ChartDef{
				{ID: "mem_pct", Title: "메모리 사용률", Stack: true, Max: 100, Overview: true},
				{ID: "mem_used", Title: "메모리 사용량"},
			},
			Metrics: []MetricDef{
				{Name: "host.mem.used_pct", Title: "사용", Unit: "%", Chart: "mem_pct", Procfs: true, Desc: "캐시를 뺀 사용(Total−Free−Buffers−Cached−SReclaimable)"},
				{Name: "host.mem.cache_pct", Title: "캐시", Unit: "%", Chart: "mem_pct", Procfs: true, Desc: "버퍼·페이지 캐시(Buffers+Cached+SReclaimable). 필요하면 회수됨"},
				{Name: "host.mem.used_cache_pct", Title: "사용+캐시", Unit: "%", Chart: "mem_pct", Procfs: true, Derived: true, Desc: "(Total−Free)/Total"},
				{Name: "host.mem.used_mb", Title: "사용", Unit: "MB", Chart: "mem_used", Procfs: true},
			}},
		{ID: "disk", Title: "디스크", Key: []string{"host.disk.read_mbps", "host.disk.write_mbps"},
			Charts: []ChartDef{
				{ID: "disk_bps", Title: "디스크 처리량", Overview: true},
				{ID: "disk_iops", Title: "디스크 IOPS"},
			},
			Metrics: []MetricDef{
				{Name: "host.disk.read_mbps", Title: "읽기", Unit: "MB/s", Chart: "disk_bps", Procfs: true, Desc: "/proc/diskstats 섹터×512, 파티션 제외"},
				{Name: "host.disk.write_mbps", Title: "쓰기", Unit: "MB/s", Chart: "disk_bps", Procfs: true},
				{Name: "host.disk.read_iops", Title: "읽기", Unit: "회/초", Chart: "disk_iops", Procfs: true},
				{Name: "host.disk.write_iops", Title: "쓰기", Unit: "회/초", Chart: "disk_iops", Procfs: true},
			}},
		{ID: "net", Title: "네트워크", Key: []string{"host.net.rx_kbps", "host.net.tx_kbps"},
			Charts: []ChartDef{
				{ID: "net_bps", Title: "네트워크 트래픽", Overview: true},
				{ID: "net_pps", Title: "네트워크 패킷"},
			},
			Metrics: []MetricDef{
				{Name: "host.net.rx_kbps", Title: "수신", Unit: "KB/s", Chart: "net_bps", Procfs: true, Desc: "/proc/net/dev, lo 제외"},
				{Name: "host.net.tx_kbps", Title: "송신", Unit: "KB/s", Chart: "net_bps", Procfs: true},
				{Name: "host.net.rx_pps", Title: "수신", Unit: "개/초", Chart: "net_pps", Procfs: true},
				{Name: "host.net.tx_pps", Title: "송신", Unit: "개/초", Chart: "net_pps", Procfs: true},
			}},
		{ID: "proc", Title: "프로세스", Key: []string{"host.proc.count"},
			Charts: []ChartDef{
				{ID: "proc_count", Title: "프로세스 수"},
				{ID: "proc_threads", Title: "스레드 수"},
				{ID: "proc_fork", Title: "프로세스 생성"},
				{ID: "job_cpu", Title: "CPU 사용률", Stack: true, Scope: "job"},
				{ID: "job_mem", Title: "메모리", Scope: "job"},
				{ID: "job_vsz", Title: "가상 메모리", Scope: "job"},
				{ID: "job_io", Title: "I/O", Scope: "job"},
				{ID: "job_threads", Title: "스레드 수", Scope: "job"},
				{ID: "job_procs", Title: "프로세스 수", Scope: "job"},
			},
			Metrics: []MetricDef{
				{Name: "host.proc.count", Title: "프로세스", Unit: "개", Chart: "proc_count", Procfs: true, Desc: "/proc의 숫자 폴더 수"},
				{Name: "host.proc.threads", Title: "스레드", Unit: "개", Chart: "proc_threads", Procfs: true, Desc: "/proc/loadavg 4번째 필드의 분모"},
				{Name: "host.sys.fork_ps", Title: "fork", Unit: "회/초", Chart: "proc_fork", Procfs: true, Desc: "/proc/stat processes 증가분"},
				{Name: "job.cpu.user_pct", Title: "사용자", Unit: "%", Chart: "job_cpu", Procfs: true, Desc: "utime. 코어 1개 = 100%"},
				{Name: "job.cpu.sys_pct", Title: "시스템", Unit: "%", Chart: "job_cpu", Procfs: true, Desc: "stime. 코어 1개 = 100%"},
				{Name: "job.cpu.pct", Title: "CPU 사용률", Unit: "%", Chart: "job_cpu", Procfs: true, Derived: true, Desc: "사용자 + 시스템"},
				{Name: "job.rss.mb", Title: "RSS", Unit: "MB", Chart: "job_mem", Procfs: true, Desc: "실제 메모리에 올라온 크기(statm resident)"},
				{Name: "job.mem.size_mb", Title: "데이터", Unit: "MB", Chart: "job_mem", Procfs: true, Desc: "데이터+스택 영역(statm data)"},
				{Name: "job.mem.vsz_mb", Title: "가상", Unit: "MB", Chart: "job_vsz", Procfs: true, Desc: "가상 주소 공간 전체(statm size, VSZ)"},
				{Name: "job.io.char_kbps", Title: "읽기+쓰기", Unit: "KB/s", Chart: "job_io", Procfs: true, Desc: "read·write 호출 바이트(/proc/<pid>/io rchar+wchar). 소켓·파이프 포함"},
				{Name: "job.threads", Title: "스레드", Unit: "개", Chart: "job_threads", Procfs: true},
				{Name: "job.procs", Title: "프로세스", Unit: "개", Chart: "job_procs", Procfs: true},
			}},
	}
}
