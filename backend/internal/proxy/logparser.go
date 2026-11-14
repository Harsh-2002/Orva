package proxy

import "github.com/Harsh-2002/Orva/backend/internal/executionlogs"

func stripNsjailNoise(stderr []byte) []byte {
	return executionlogs.StripNsjailNoise(stderr)
}
