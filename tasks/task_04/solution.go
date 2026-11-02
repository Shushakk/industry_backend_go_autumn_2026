package main

type Stats struct {
	Count         int
	Sum, Min, Max int64
}

func Calc(nums []int64) Stats {
	if len(nums) < 2 {
		return Stats{}
	}
	diff := nums[1] - nums[0]

	stats := Stats{
		Count: 1,
		Sum:   diff,
		Min:   diff,
		Max:   diff,
	}

	for i := 2; i < len(nums); i++ {
		diff := nums[i] - nums[i-1]

		stats.Count++
		stats.Sum += diff

		if diff < stats.Min {
			stats.Min = diff
		}

		if diff > stats.Max {
			stats.Max = diff
		}
	}
	return stats
}
