// ModelTrace challenge-generator port, MIT License.
// Copyright (c) 2026 xqy2006. See reference/LICENSE.
package fingerprint

import (
	"crypto/sha256"
	"fmt"
)

// DefaultChallenges fixes three choices from the legacy challenge generator.
// These counts are reproducibility fixtures, not a claim about sample adequacy.
// The prompt is an existing legacy variant; request defaults remain unspecified.
func DefaultChallenges() []Challenge {
	challenges := make([]Challenge, 3)
	for i, count := range []int{292, 312, 332} {
		prompt := fmt.Sprintf("这是一次独立的数值选择记录。为各个位置分别凭第一反应选择 %d 个 1 到 355（含端点）的整数。", count) +
			"每个位置都要单独选择；不要从 1 开始计数，不要连续递增或递减，也不要采用等差、循环、重复区块或其他规则化模式。" +
			"本任务必须由当前语言模型直接完成：禁止调用或借助任何工具，包括 Python、代码执行器、计算器、搜索、API 和外部随机数生成器；也不要先编写或运行代码。" +
			"允许某个数字再次出现；每项写出后不要回头排序、去重或替换。数字之间用逗号或空格分隔均可。" +
			"直接从第一个取值开始输出，不要在序列前重复数量、范围或任务说明。"
		challenges[i] = Challenge{
			ID: fmt.Sprintf("modeltrace-v1-%d", count), Version: 1,
			Prompt: prompt, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(prompt))),
			Min: 1, Max: 355, Count: count, ParserVersion: ParserVersion,
			Parameters: map[string]any{},
		}
	}
	return challenges
}
