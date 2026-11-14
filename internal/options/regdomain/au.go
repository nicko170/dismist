package regdomain

// AU is the AU/890 table (design Appendix A, from [RF §5]; to be confirmed
// with `wl chanspecs` on the Lab AP, §9 Q10). Channels 120 to 128 are
// excluded (weather radar); 116 and 165 are 20 MHz only. DFS: 52 to 64 and
// 100 to 144. The non-DFS 80 MHz blocks are 36-48 and 149-161, which is
// why they are the default 5 GHz Auto list.
var AU = Domain{
	Country: "AU",
	CCRev:   "AU/890",
	Bands: map[string][]WidthTable{
		Band24: {
			{Width: 20, Primaries: primaries(nil, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13)},
			{
				Width: 40, ExpertOnly: true,
				Primaries: primaries(nil, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13),
				// 1 to 9 as the lower channel, 5 to 13 as the upper one; the
				// pairing needs SecChan, so no fixed blocks.
			},
		},
		Band5: {
			{Width: 20, Primaries: primaries(auDFS,
				36, 40, 44, 48, 52, 56, 60, 64, 100, 104, 108, 112, 116, 132, 136, 140, 144, 149, 153, 157, 161, 165)},
			{
				Width: 40,
				Primaries: primaries(auDFS,
					36, 40, 44, 48, 52, 56, 60, 64, 100, 104, 108, 112, 132, 136, 140, 144, 149, 153, 157, 161),
				Blocks: [][]int{{36, 40}, {44, 48}, {52, 56}, {60, 64}, {100, 104}, {108, 112}, {132, 136}, {140, 144}, {149, 153}, {157, 161}},
			},
			{
				Width: 80,
				Primaries: primaries(auDFS,
					36, 40, 44, 48, 52, 56, 60, 64, 100, 104, 108, 112, 132, 136, 140, 144, 149, 153, 157, 161),
				Blocks: [][]int{{36, 40, 44, 48}, {52, 56, 60, 64}, {100, 104, 108, 112}, {132, 136, 140, 144}, {149, 153, 157, 161}},
			},
			{
				Width: 160, NeedsLabTest: true,
				Primaries: primaries(auDFS, 36, 40, 44, 48, 52, 56, 60, 64),
				Blocks:    [][]int{{36, 40, 44, 48, 52, 56, 60, 64}},
			},
		},
	},
	Recommended: map[string][]int{Band24: {1, 6, 11}, Band5: {36, 40, 44, 48, 149, 153, 157, 161}},
	DefaultAuto: map[string][]int{Band24: {1, 6, 11}, Band5: {36, 40, 44, 48, 149, 153, 157, 161}},
}

// auDFS reports the AU DFS channels: 52 to 64 and 100 to 144.
func auDFS(channel int) bool {
	return (channel >= 52 && channel <= 64) || (channel >= 100 && channel <= 144)
}

func primaries(dfs func(int) bool, channels ...int) []Primary {
	result := make([]Primary, 0, len(channels))
	for _, channel := range channels {
		result = append(result, Primary{Channel: channel, DFS: dfs != nil && dfs(channel)})
	}
	return result
}
