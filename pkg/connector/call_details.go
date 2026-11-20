package connector

import "strings"

// CallDetails is the technical half of a refused call's message: which
// connector, who the call ran as, whether it ran on a lend, and which
// account it picked. It never carries a credential — ids and display
// names only — so it is safe to hand to the model and to a person.
type CallDetails struct {
	ConnectorLabel string
	ConnectorID    string
	// CallerID is the effective caller: the lender when the call is lent.
	CallerID string
	// LenderID is set when the call runs on a lend; RequesterID is who
	// actually asked for it.
	LenderID    string
	RequesterID string
	// AccountID is the connected account the call picked ("" = none);
	// AccountOwnerID is the wick user it belongs to.
	AccountID      string
	AccountOwnerID string
	// Name resolves a user id to a display name ("" when unknown). nil =
	// ids only.
	Name func(userID string) string
}

// Who is "id (Name)", "id" when the name is unknown, "nobody" for "".
func (d CallDetails) Who(id string) string {
	if id == "" {
		return "nobody"
	}
	if d.Name != nil {
		if n := strings.TrimSpace(d.Name(id)); n != "" && n != id {
			return id + " (" + n + ")"
		}
	}
	return id
}

// String renders the details as one bracketed line appended after the
// short human sentence.
func (d CallDetails) String() string {
	parts := []string{"connector " + d.ConnectorLabel + " (" + d.ConnectorID + ")", "runs as " + d.Who(d.CallerID)}
	if d.LenderID != "" {
		parts = append(parts, "lent by "+d.Who(d.LenderID)+" to "+d.Who(d.RequesterID))
	} else {
		parts = append(parts, "not lent")
	}
	if d.AccountID == "" {
		parts = append(parts, "no account picked")
	} else {
		parts = append(parts, "account "+d.AccountID+" owned by "+d.Who(d.AccountOwnerID))
	}
	return "[details: " + strings.Join(parts, " · ") + "]"
}
