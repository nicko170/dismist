package telemetry

// Health sections: 24 Proc, 48 CloudStats, 80 Environment and
// 84 PaceRuntime (design §5.2). The scalar health fields (5, 21, 23, 70,
// 71, 75, 93) are read in decodeAPStats.

// maxKMsgLines bounds the kernel log lines taken from one frame.
const maxKMsgLines = 200

// decodeProc reads Proc (24): 1 KMsg []string, 2 MemInfo {1 Total_kB,
// 2 Available_kB}, 4 LoadAvg {1 Load [3]float32 (packed), 2 Running,
// 3 Total}, 6 FS {1 RW {1 Total_kB, 2 Available_kB}}, 7 Stat {1 TotalCPU,
// 2 CPUs []CPUStats}. It reports whether anything was read.
func decodeProc(r reader, f field, h *Health, update *Update) bool {
	fields, ok := r.message(f)
	if !ok {
		return false
	}
	seen := false
	for _, item := range fields {
		switch item.number {
		case 1:
			if line := r.in(SectionDiag).str(item, 512); line != "" && len(update.KMsg) < maxKMsgLines {
				update.KMsg = append(update.KMsg, line)
			}
		case 2:
			sub, ok := r.message(item)
			if !ok {
				continue
			}
			var memory Memory
			for _, value := range sub {
				switch value.number {
				case 1:
					memory.TotalKB = r.uint(value)
				case 2:
					memory.AvailableKB = r.uint(value)
				}
			}
			h.Memory, seen = &memory, true
		case 4:
			sub, ok := r.message(item)
			if !ok {
				continue
			}
			var load Load
			averages := 0
			for _, value := range sub {
				switch value.number {
				case 1:
					for _, average := range r.floats32(value) {
						if averages < 3 {
							load.Average[averages] = average
							averages++
						}
					}
				case 2:
					load.Running = uint32(min(r.uint(value), 1<<31))
				case 3:
					load.Total = uint32(min(r.uint(value), 1<<31))
				}
			}
			h.Load, seen = &load, true
		case 6:
			sub, ok := r.message(item)
			if !ok {
				continue
			}
			for _, value := range sub {
				if value.number != 1 {
					continue
				}
				rw, ok := r.message(value)
				if !ok {
					continue
				}
				var fs Filesystem
				for _, part := range rw {
					switch part.number {
					case 1:
						fs.TotalKB = r.uint(part)
					case 2:
						fs.AvailableKB = r.uint(part)
					}
				}
				h.RW, seen = &fs, true
			}
		case 7:
			sub, ok := r.message(item)
			if !ok {
				continue
			}
			for _, value := range sub {
				switch value.number {
				case 1:
					if cpu, ok := decodeCPU(r, value); ok {
						h.CPU, seen = &cpu, true
					}
				case 2:
					if cpu, ok := decodeCPU(r, value); ok && len(h.CPUs) < 64 {
						h.CPUs, seen = append(h.CPUs, cpu), true
					}
				}
			}
		}
	}
	return seen
}

// decodeCPU reads CPUStats: 1 Name, then 2 to 9 the jiffies by mode.
func decodeCPU(r reader, f field) (CPUTimes, bool) {
	fields, ok := r.message(f)
	if !ok {
		return CPUTimes{}, false
	}
	var cpu CPUTimes
	for _, item := range fields {
		switch item.number {
		case 1:
			cpu.Name = r.str(item, 16)
		case 2:
			cpu.User = r.uint(item)
		case 3:
			cpu.Nice = r.uint(item)
		case 4:
			cpu.System = r.uint(item)
		case 5:
			cpu.Idle = r.uint(item)
		case 6:
			cpu.IOWait = r.uint(item)
		case 7:
			cpu.IRQ = r.uint(item)
		case 8:
			cpu.SoftIRQ = r.uint(item)
		case 9:
			cpu.Steal = r.uint(item)
		}
	}
	return cpu, true
}

// decodeCloud reads CloudStats (48) in both layouts: 0.8 and 0.14 send
// 1 LastRTT (float32 s) and 2 WhenLastRTT; 0.15 sends 3 PMTU, 4 SendMSS,
// 5 CachedIP, 6 WebSockRTT, 7 WhenWebSockRTT and 8 TCPSockRTT.
func decodeCloud(r reader, f field) (*Cloud, bool) {
	fields, ok := r.message(f)
	if !ok {
		return nil, false
	}
	var cloud Cloud
	seconds := func(item field) *float64 {
		value := r.float32(item)
		if value <= 0 || value > 3600 {
			return nil
		}
		return &value
	}
	for _, item := range fields {
		switch item.number {
		case 1:
			cloud.LastRTTSeconds = seconds(item)
		case 3:
			cloud.PMTU = uint32(min(r.uint(item), 1<<20))
		case 4:
			cloud.SendMSS = uint32(min(r.uint(item), 1<<20))
		case 5:
			cloud.CachedIP = r.bool(item)
		case 6:
			cloud.WebSocketRTTSeconds = seconds(item)
		case 8:
			cloud.TCPRTTSeconds = seconds(item)
		}
	}
	return &cloud, true
}

// decodeEnvironment reads Environment (80): 1 Orientation {1 Accel {1 X,
// 2 Y, 3 Z float32}}, 2 Temperature {1 Ambient, 2 CPU float32}.
func decodeEnvironment(r reader, f field, h *Health) bool {
	fields, ok := r.message(f)
	if !ok {
		return false
	}
	seen := false
	for _, item := range fields {
		switch item.number {
		case 1:
			orientation, ok := r.message(item)
			if !ok {
				continue
			}
			for _, part := range orientation {
				if part.number != 1 {
					continue
				}
				axes, ok := r.message(part)
				if !ok {
					continue
				}
				var accel Vector3
				for _, axis := range axes {
					switch axis.number {
					case 1:
						accel.X = r.float32(axis)
					case 2:
						accel.Y = r.float32(axis)
					case 3:
						accel.Z = r.float32(axis)
					}
				}
				h.Accel, seen = &accel, true
			}
		case 2:
			sensors, ok := r.message(item)
			if !ok {
				continue
			}
			var temperature Temperature
			for _, sensor := range sensors {
				value := r.float32(sensor)
				if value < -60 || value > 200 {
					continue
				}
				switch sensor.number {
				case 1:
					temperature.Ambient = &value
				case 2:
					temperature.CPU = &value
				}
			}
			if temperature.Ambient != nil || temperature.CPU != nil {
				h.Temperature, seen = &temperature, true
			}
		}
	}
	return seen
}

// decodeAgent reads PaceRuntimeStats (84): 1 Alloc, 2 NumGoRoutine,
// 3 GoRssApprox, 4 HeapUnusedButKept.
func decodeAgent(r reader, f field) (*Agent, bool) {
	fields, ok := r.message(f)
	if !ok {
		return nil, false
	}
	var agent Agent
	for _, item := range fields {
		switch item.number {
		case 1:
			agent.AllocBytes = r.uint(item)
		case 2:
			agent.Goroutines = uint32(min(r.uint(item), 1<<31))
		case 3:
			agent.RSSBytes = r.uint(item)
		case 4:
			agent.HeapUnusedBytes = r.uint(item)
		}
	}
	return &agent, true
}
