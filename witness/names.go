package main

import "strings"

func programNames(platform string) (string, string) {
	switch platform {
	case "windows":
		return "Ark0Witness.exe", "bitcoind.exe"
	case "darwin":
		return "Ark0Witness", "bitcoind"
	default:
		return "ark0-witness", "bitcoind"
	}
}

func fillProgramNames(text, platform string) string {
	program, node := programNames(platform)
	return strings.NewReplacer("{program}", program, "{node}", node).Replace(text)
}
