package modelchannel

import "strings"

func IsMetaModelAlias(modelRef string) bool {
	switch strings.ToLower(strings.TrimSpace(modelRef)) {
	case "fast", "default", "auto":
		return true
	default:
		return false
	}
}

func ResolveAdapterIndexes[T any](adapters []T, requestedModelRef string, id func(T) string, providerModelID func(T) string, legacyIDs ...func(T) string) []int {
	if len(adapters) == 0 {
		return nil
	}
	target := strings.TrimSpace(requestedModelRef)
	if target == "" || IsMetaModelAlias(target) {
		target = strings.TrimSpace(id(adapters[0]))
	}
	if target == "" {
		return nil
	}
	matches := make([]int, 0, len(adapters))
	for index, adapter := range adapters {
		if strings.TrimSpace(id(adapter)) == target {
			matches = append(matches, index)
		}
	}
	if len(matches) > 0 {
		return matches
	}
	for _, legacyID := range legacyIDs {
		if legacyID == nil {
			continue
		}
		for index, adapter := range adapters {
			if strings.TrimSpace(legacyID(adapter)) == target {
				matches = append(matches, index)
			}
		}
		if len(matches) > 0 {
			return matches
		}
	}
	for index, adapter := range adapters {
		if strings.TrimSpace(providerModelID(adapter)) == target {
			matches = append(matches, index)
		}
	}
	return matches
}

func ResolveAdapterIndex[T any](adapters []T, requestedModelRef string, id func(T) string, providerModelID func(T) string, legacyIDs ...func(T) string) (int, bool) {
	matches := ResolveAdapterIndexes(adapters, requestedModelRef, id, providerModelID, legacyIDs...)
	if len(matches) != 1 {
		return -1, false
	}
	return matches[0], true
}
