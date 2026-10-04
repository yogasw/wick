package view

import "github.com/yogasw/wick/internal/entity"

func shortID(id string) string {
	if len(id) > 11 {
		return id[:4] + "…" + id[len(id)-4:]
	}
	return id
}

// viewerEmail is the signed-in user's email for the Team account menu, or
// "" when nobody is signed in.
func viewerEmail(u *entity.User) string {
	if u == nil {
		return ""
	}
	return u.Email
}
