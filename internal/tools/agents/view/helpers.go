package view

func shortID(id string) string {
	if len(id) > 11 {
		return id[:4] + "…" + id[len(id)-4:]
	}
	return id
}
