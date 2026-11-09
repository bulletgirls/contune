// 05 §2·§3 카탈로그(internal/model/catalog.go의 Catalog()와 같은 내용)
export const CATALOG = {
  "groups": [
    {
      "id": "cpu",
      "title": "CPU",
      "metrics": [
        {
          "name": "host.cpu.pct",
          "title": "User+Sys",
          "unit": "%",
          "chart": "cpu_us",
          "procfs": true
        },
        {
          "name": "host.cpu.user_pct",
          "title": "User",
          "unit": "%",
          "chart": "cpu_us",
          "procfs": true
        },
        {
          "name": "host.cpu.sys_pct",
          "title": "Sys",
          "unit": "%",
          "chart": "cpu_us",
          "procfs": true
        },
        {
          "name": "host.cpu.wait_pct",
          "title": "Wait",
          "unit": "%",
          "chart": "cpu_wait",
          "procfs": true
        },
        {
          "name": "host.cpu.idle_pct",
          "title": "Idle",
          "unit": "%",
          "chart": "cpu_idle",
          "procfs": true
        },
        {
          "name": "host.cpu.steal_pct",
          "title": "Stolen",
          "unit": "%",
          "chart": "cpu_wait",
          "procfs": true
        },
        {
          "name": "host.cpu.busy_wait_pct",
          "title": "User+Sys+Wait",
          "unit": "%",
          "chart": "cpu_busy",
          "procfs": true
        },
        {
          "name": "host.proc.running",
          "title": "실행 중 프로세스",
          "unit": "개",
          "chart": "proc_running",
          "procfs": true
        }
      ]
    },
    {
      "id": "mem",
      "title": "메모리",
      "metrics": [
        {
          "name": "host.mem.used_pct",
          "title": "사용",
          "unit": "%",
          "chart": "mem_pct",
          "procfs": true
        },
        {
          "name": "host.mem.used_mb",
          "title": "사용",
          "unit": "MB",
          "chart": "mem_mb",
          "procfs": true
        },
        {
          "name": "host.mem.cache_pct",
          "title": "캐시",
          "unit": "%",
          "chart": "mem_pct",
          "procfs": true
        },
        {
          "name": "host.mem.used_cache_pct",
          "title": "사용+캐시",
          "unit": "%",
          "chart": "mem_pct",
          "procfs": true
        }
      ]
    },
    {
      "id": "sys",
      "title": "시스템",
      "metrics": [
        {
          "name": "host.sys.fork_ps",
          "title": "Fork",
          "unit": "회/초",
          "chart": "sys_fork",
          "procfs": true
        },
        {
          "name": "host.sys.intr_ps",
          "title": "인터럽트",
          "unit": "회/초",
          "chart": "sys_intr",
          "procfs": true
        },
        {
          "name": "host.sys.syscall_ps",
          "title": "시스템 콜",
          "unit": "회/초",
          "chart": "sys_syscall",
          "procfs": false
        },
        {
          "name": "host.sys.ctxsw_ps",
          "title": "문맥 교환",
          "unit": "회/초",
          "chart": "sys_ctxsw",
          "procfs": true
        },
        {
          "name": "host.proc.count",
          "title": "프로세스",
          "unit": "개",
          "chart": "proc_count",
          "procfs": true
        },
        {
          "name": "host.proc.threads",
          "title": "스레드",
          "unit": "개",
          "chart": "proc_threads",
          "procfs": true
        },
        {
          "name": "host.load1",
          "title": "Load 1분",
          "unit": "",
          "chart": "load",
          "procfs": true
        }
      ]
    },
    {
      "id": "proc",
      "title": "프로세스",
      "metrics": [
        {
          "name": "job.cpu.pct",
          "title": "User+Sys",
          "unit": "%",
          "chart": "job_cpu",
          "procfs": true
        },
        {
          "name": "job.cpu.user_pct",
          "title": "User",
          "unit": "%",
          "chart": "job_cpu",
          "procfs": true
        },
        {
          "name": "job.cpu.sys_pct",
          "title": "Sys",
          "unit": "%",
          "chart": "job_cpu",
          "procfs": true
        },
        {
          "name": "job.mem.size_mb",
          "title": "Size",
          "unit": "MB",
          "chart": "job_mem",
          "procfs": true
        },
        {
          "name": "job.rss.mb",
          "title": "RSS",
          "unit": "MB",
          "chart": "job_mem",
          "procfs": true
        },
        {
          "name": "job.mem.vsz_mb",
          "title": "Virtual Memory",
          "unit": "MB",
          "chart": "job_vsz",
          "procfs": true
        },
        {
          "name": "job.io.char_kbps",
          "title": "Char IO",
          "unit": "KB/s",
          "chart": "job_io",
          "procfs": true
        },
        {
          "name": "job.threads",
          "title": "스레드",
          "unit": "개",
          "chart": "job_threads",
          "procfs": true
        },
        {
          "name": "job.procs",
          "title": "프로세스",
          "unit": "개",
          "chart": "job_procs",
          "procfs": true
        }
      ]
    },
    {
      "id": "io",
      "title": "IO",
      "metrics": [
        {
          "name": "host.disk.read_mbps",
          "title": "디스크 읽기",
          "unit": "MB/s",
          "chart": "disk_rw",
          "procfs": true
        },
        {
          "name": "host.disk.write_mbps",
          "title": "디스크 쓰기",
          "unit": "MB/s",
          "chart": "disk_rw",
          "procfs": true
        },
        {
          "name": "host.disk.read_iops",
          "title": "디스크 읽기 IOPS",
          "unit": "IO/s",
          "chart": "disk_iops",
          "procfs": true
        },
        {
          "name": "host.disk.write_iops",
          "title": "디스크 쓰기 IOPS",
          "unit": "IO/s",
          "chart": "disk_iops",
          "procfs": true
        },
        {
          "name": "host.net.rx_kbps",
          "title": "네트워크 수신",
          "unit": "KB/s",
          "chart": "net_kbps",
          "procfs": true
        },
        {
          "name": "host.net.tx_kbps",
          "title": "네트워크 송신",
          "unit": "KB/s",
          "chart": "net_kbps",
          "procfs": true
        },
        {
          "name": "host.net.rx_pps",
          "title": "수신 패킷",
          "unit": "개/초",
          "chart": "net_pps",
          "procfs": true
        },
        {
          "name": "host.net.tx_pps",
          "title": "송신 패킷",
          "unit": "개/초",
          "chart": "net_pps",
          "procfs": true
        }
      ]
    }
  ]
};
