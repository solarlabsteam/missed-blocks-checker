package main

func stringInSlice(first string, list []string) bool {
	for _, second := range list {
		if first == second {
			return true
		}
	}
	return false
}

func removeFromSlice(slice []string, r string) []string {
	for i, v := range slice {
		if v == r {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}
