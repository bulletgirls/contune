package model

// MetricDef는 카탈로그의 지표 항목이다. Chart는 같은 차트에 겹쳐 그릴 묶음 이름이다(단위가 같을 때만).
type MetricDef struct {
	Name   string `json:"name"`
	Title  string `json:"title"`
	Unit   string `json:"unit"`
	Chart  string `json:"chart"`
	Procfs bool   `json:"procfs"` // procfs 수집기가 내는가
}

// MetricGroup은 서버 탭의 서브 탭 하나다.
type MetricGroup struct {
	ID      string      `json:"id"`
	Title   string      `json:"title"`
	Metrics []MetricDef `json:"metrics"`
}

// Catalog는 서버 탭의 서브 탭과 항목이다(05 §2·§3). 항목을 늘릴 때는 여기에 한 줄을 더한다.
func Catalog() []MetricGroup {
	return []MetricGroup{
		{ID: "cpu", Title: "CPU", Metrics: []MetricDef{
			{"host.cpu.pct", "User+Sys", "%", "cpu_us", true},
			{"host.cpu.user_pct", "User", "%", "cpu_us", true},
			{"host.cpu.sys_pct", "Sys", "%", "cpu_us", true},
			{"host.cpu.wait_pct", "Wait", "%", "cpu_wait", true},
			{"host.cpu.idle_pct", "Idle", "%", "cpu_idle", true},
			{"host.cpu.steal_pct", "Stolen", "%", "cpu_wait", true},
			{"host.cpu.busy_wait_pct", "User+Sys+Wait", "%", "cpu_busy", true},
			{"host.proc.running", "실행 중 프로세스", "개", "proc_running", true},
		}},
		{ID: "mem", Title: "메모리", Metrics: []MetricDef{
			{"host.mem.used_pct", "사용", "%", "mem_pct", true},
			{"host.mem.used_mb", "사용", "MB", "mem_mb", true},
			{"host.mem.cache_pct", "캐시", "%", "mem_pct", true},
			{"host.mem.used_cache_pct", "사용+캐시", "%", "mem_pct", true},
		}},
		{ID: "sys", Title: "시스템", Metrics: []MetricDef{
			{"host.sys.fork_ps", "Fork", "회/초", "sys_fork", true},
			{"host.sys.intr_ps", "인터럽트", "회/초", "sys_intr", true},
			{"host.sys.syscall_ps", "시스템 콜", "회/초", "sys_syscall", false},
			{"host.sys.ctxsw_ps", "문맥 교환", "회/초", "sys_ctxsw", true},
			{"host.proc.count", "프로세스", "개", "proc_count", true},
			{"host.proc.threads", "스레드", "개", "proc_threads", true},
			{"host.load1", "Load 1분", "", "load", true},
		}},
		{ID: "proc", Title: "프로세스", Metrics: []MetricDef{
			{"job.cpu.pct", "User+Sys", "%", "job_cpu", true},
			{"job.cpu.user_pct", "User", "%", "job_cpu", true},
			{"job.cpu.sys_pct", "Sys", "%", "job_cpu", true},
			{"job.mem.size_mb", "Size", "MB", "job_mem", true},
			{"job.rss.mb", "RSS", "MB", "job_mem", true},
			{"job.mem.vsz_mb", "Virtual Memory", "MB", "job_vsz", true},
			{"job.io.char_kbps", "Char IO", "KB/s", "job_io", true},
			{"job.threads", "스레드", "개", "job_threads", true},
			{"job.procs", "프로세스", "개", "job_procs", true},
		}},
		{ID: "io", Title: "IO", Metrics: []MetricDef{
			{"host.disk.read_mbps", "디스크 읽기", "MB/s", "disk_rw", true},
			{"host.disk.write_mbps", "디스크 쓰기", "MB/s", "disk_rw", true},
			{"host.disk.read_iops", "디스크 읽기 IOPS", "IO/s", "disk_iops", true},
			{"host.disk.write_iops", "디스크 쓰기 IOPS", "IO/s", "disk_iops", true},
			{"host.net.rx_kbps", "네트워크 수신", "KB/s", "net_kbps", true},
			{"host.net.tx_kbps", "네트워크 송신", "KB/s", "net_kbps", true},
			{"host.net.rx_pps", "수신 패킷", "개/초", "net_pps", true},
			{"host.net.tx_pps", "송신 패킷", "개/초", "net_pps", true},
		}},
	}
}
