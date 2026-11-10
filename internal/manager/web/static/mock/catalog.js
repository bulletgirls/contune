// 05 §2·§3 카탈로그(internal/model/catalog.go의 Catalog()와 같은 내용)
export const CATALOG = {
  "groups": [
    {
      "id": "cpu",
      "title": "CPU",
      "key": [
        "host.cpu.pct"
      ],
      "charts": [
        {
          "id": "cpu_use",
          "title": "CPU 사용률",
          "stack": true,
          "max": 100,
          "overview": true
        },
        {
          "id": "cpu_load",
          "title": "부하 평균"
        },
        {
          "id": "cpu_runq",
          "title": "실행 큐"
        },
        {
          "id": "cpu_intr",
          "title": "인터럽트"
        },
        {
          "id": "cpu_ctxsw",
          "title": "컨텍스트 스위치"
        },
        {
          "id": "cpu_syscall",
          "title": "시스템 콜"
        }
      ],
      "metrics": [
        {
          "name": "host.cpu.user_pct",
          "title": "사용자",
          "unit": "%",
          "chart": "cpu_use",
          "procfs": true,
          "desc": "사용자 모드 시간(user+nice)"
        },
        {
          "name": "host.cpu.sys_pct",
          "title": "시스템",
          "unit": "%",
          "chart": "cpu_use",
          "procfs": true,
          "desc": "커널 모드 시간(system+irq+softirq)"
        },
        {
          "name": "host.cpu.wait_pct",
          "title": "I/O 대기",
          "unit": "%",
          "chart": "cpu_use",
          "procfs": true,
          "desc": "디스크 I/O를 기다리며 쉰 시간(iowait)"
        },
        {
          "name": "host.cpu.steal_pct",
          "title": "스틸",
          "unit": "%",
          "chart": "cpu_use",
          "procfs": true,
          "desc": "가상 머신에서 하이퍼바이저가 가져간 시간(steal)"
        },
        {
          "name": "host.cpu.pct",
          "title": "CPU 사용률",
          "unit": "%",
          "chart": "cpu_use",
          "procfs": true,
          "derived": true,
          "desc": "사용자 + 시스템"
        },
        {
          "name": "host.cpu.busy_wait_pct",
          "title": "사용률+I/O 대기",
          "unit": "%",
          "chart": "cpu_use",
          "procfs": true,
          "derived": true,
          "desc": "사용자 + 시스템 + I/O 대기"
        },
        {
          "name": "host.cpu.idle_pct",
          "title": "유휴",
          "unit": "%",
          "chart": "cpu_use",
          "procfs": true,
          "derived": true,
          "desc": "100 − 나머지"
        },
        {
          "name": "host.load1",
          "title": "1분",
          "unit": "",
          "chart": "cpu_load",
          "procfs": true,
          "desc": "1분 부하 평균(/proc/loadavg). 코어 수보다 크면 CPU를 기다리는 작업이 있음"
        },
        {
          "name": "host.proc.running",
          "title": "실행 큐",
          "unit": "개",
          "chart": "cpu_runq",
          "procfs": true,
          "desc": "실행 중이거나 CPU를 기다리는 프로세스 수(vmstat r, /proc/stat procs_running)"
        },
        {
          "name": "host.sys.intr_ps",
          "title": "인터럽트",
          "unit": "회/초",
          "chart": "cpu_intr",
          "procfs": true
        },
        {
          "name": "host.sys.ctxsw_ps",
          "title": "컨텍스트 스위치",
          "unit": "회/초",
          "chart": "cpu_ctxsw",
          "procfs": true
        },
        {
          "name": "host.sys.syscall_ps",
          "title": "시스템 콜",
          "unit": "회/초",
          "chart": "cpu_syscall",
          "procfs": false,
          "desc": "procfs로는 셀 수 없음(확장: eBPF)"
        }
      ]
    },
    {
      "id": "mem",
      "title": "메모리",
      "key": [
        "host.mem.used_pct"
      ],
      "charts": [
        {
          "id": "mem_pct",
          "title": "메모리 사용률",
          "stack": true,
          "max": 100,
          "overview": true
        },
        {
          "id": "mem_used",
          "title": "메모리 사용량"
        }
      ],
      "metrics": [
        {
          "name": "host.mem.used_pct",
          "title": "사용",
          "unit": "%",
          "chart": "mem_pct",
          "procfs": true,
          "desc": "캐시를 뺀 사용(Total−Free−Buffers−Cached−SReclaimable)"
        },
        {
          "name": "host.mem.cache_pct",
          "title": "캐시",
          "unit": "%",
          "chart": "mem_pct",
          "procfs": true,
          "desc": "버퍼·페이지 캐시(Buffers+Cached+SReclaimable). 필요하면 회수됨"
        },
        {
          "name": "host.mem.used_cache_pct",
          "title": "사용+캐시",
          "unit": "%",
          "chart": "mem_pct",
          "procfs": true,
          "derived": true,
          "desc": "(Total−Free)/Total"
        },
        {
          "name": "host.mem.used_mb",
          "title": "사용",
          "unit": "MB",
          "chart": "mem_used",
          "procfs": true
        }
      ]
    },
    {
      "id": "disk",
      "title": "디스크",
      "key": [
        "host.disk.read_mbps",
        "host.disk.write_mbps"
      ],
      "charts": [
        {
          "id": "disk_bps",
          "title": "디스크 처리량",
          "overview": true
        },
        {
          "id": "disk_iops",
          "title": "디스크 IOPS"
        }
      ],
      "metrics": [
        {
          "name": "host.disk.read_mbps",
          "title": "읽기",
          "unit": "MB/s",
          "chart": "disk_bps",
          "procfs": true,
          "desc": "/proc/diskstats 섹터×512, 파티션 제외"
        },
        {
          "name": "host.disk.write_mbps",
          "title": "쓰기",
          "unit": "MB/s",
          "chart": "disk_bps",
          "procfs": true
        },
        {
          "name": "host.disk.read_iops",
          "title": "읽기",
          "unit": "회/초",
          "chart": "disk_iops",
          "procfs": true
        },
        {
          "name": "host.disk.write_iops",
          "title": "쓰기",
          "unit": "회/초",
          "chart": "disk_iops",
          "procfs": true
        }
      ]
    },
    {
      "id": "net",
      "title": "네트워크",
      "key": [
        "host.net.rx_kbps",
        "host.net.tx_kbps"
      ],
      "charts": [
        {
          "id": "net_bps",
          "title": "네트워크 트래픽",
          "overview": true
        },
        {
          "id": "net_pps",
          "title": "네트워크 패킷"
        }
      ],
      "metrics": [
        {
          "name": "host.net.rx_kbps",
          "title": "수신",
          "unit": "KB/s",
          "chart": "net_bps",
          "procfs": true,
          "desc": "/proc/net/dev, lo 제외"
        },
        {
          "name": "host.net.tx_kbps",
          "title": "송신",
          "unit": "KB/s",
          "chart": "net_bps",
          "procfs": true
        },
        {
          "name": "host.net.rx_pps",
          "title": "수신",
          "unit": "개/초",
          "chart": "net_pps",
          "procfs": true
        },
        {
          "name": "host.net.tx_pps",
          "title": "송신",
          "unit": "개/초",
          "chart": "net_pps",
          "procfs": true
        }
      ]
    },
    {
      "id": "proc",
      "title": "프로세스",
      "key": [
        "host.proc.count"
      ],
      "charts": [
        {
          "id": "proc_count",
          "title": "프로세스 수"
        },
        {
          "id": "proc_threads",
          "title": "스레드 수"
        },
        {
          "id": "proc_fork",
          "title": "프로세스 생성"
        },
        {
          "id": "job_cpu",
          "title": "CPU 사용률",
          "stack": true,
          "scope": "job"
        },
        {
          "id": "job_mem",
          "title": "메모리",
          "scope": "job"
        },
        {
          "id": "job_vsz",
          "title": "가상 메모리",
          "scope": "job"
        },
        {
          "id": "job_io",
          "title": "I/O",
          "scope": "job"
        },
        {
          "id": "job_threads",
          "title": "스레드 수",
          "scope": "job"
        },
        {
          "id": "job_procs",
          "title": "프로세스 수",
          "scope": "job"
        }
      ],
      "metrics": [
        {
          "name": "host.proc.count",
          "title": "프로세스",
          "unit": "개",
          "chart": "proc_count",
          "procfs": true,
          "desc": "/proc의 숫자 폴더 수"
        },
        {
          "name": "host.proc.threads",
          "title": "스레드",
          "unit": "개",
          "chart": "proc_threads",
          "procfs": true,
          "desc": "/proc/loadavg 4번째 필드의 분모"
        },
        {
          "name": "host.sys.fork_ps",
          "title": "fork",
          "unit": "회/초",
          "chart": "proc_fork",
          "procfs": true,
          "desc": "/proc/stat processes 증가분"
        },
        {
          "name": "job.cpu.user_pct",
          "title": "사용자",
          "unit": "%",
          "chart": "job_cpu",
          "procfs": true,
          "desc": "utime. 코어 1개 = 100%"
        },
        {
          "name": "job.cpu.sys_pct",
          "title": "시스템",
          "unit": "%",
          "chart": "job_cpu",
          "procfs": true,
          "desc": "stime. 코어 1개 = 100%"
        },
        {
          "name": "job.cpu.pct",
          "title": "CPU 사용률",
          "unit": "%",
          "chart": "job_cpu",
          "procfs": true,
          "derived": true,
          "desc": "사용자 + 시스템"
        },
        {
          "name": "job.rss.mb",
          "title": "RSS",
          "unit": "MB",
          "chart": "job_mem",
          "procfs": true,
          "desc": "실제 메모리에 올라온 크기(statm resident)"
        },
        {
          "name": "job.mem.size_mb",
          "title": "데이터",
          "unit": "MB",
          "chart": "job_mem",
          "procfs": true,
          "desc": "데이터+스택 영역(statm data)"
        },
        {
          "name": "job.mem.vsz_mb",
          "title": "가상",
          "unit": "MB",
          "chart": "job_vsz",
          "procfs": true,
          "desc": "가상 주소 공간 전체(statm size, VSZ)"
        },
        {
          "name": "job.io.char_kbps",
          "title": "읽기+쓰기",
          "unit": "KB/s",
          "chart": "job_io",
          "procfs": true,
          "desc": "read·write 호출 바이트(/proc/\u003cpid\u003e/io rchar+wchar). 소켓·파이프 포함"
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
    }
  ]
};
