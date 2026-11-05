package nte

const DefaultGameID = "1289"

// Account contains one Tajiduo account. RoleID is used as a fallback when the
// role list is unavailable; normally roles are discovered automatically.
type Account struct {
	RefreshToken string
	UID          string
	DeviceID     string
	GameID       string
	RoleID       string
}

type Outcome struct {
	Success       bool
	AlreadySigned bool
	Message       string
	Reward        string
	RewardWarning string
}

type RoleOutcome struct {
	RoleID   string
	RoleName string
	Outcome  Outcome
}

type Report struct {
	UID             string
	NewRefreshToken string
	Community       Outcome
	Roles           []RoleOutcome
}

func (r Report) OK() bool {
	if !r.Community.Success || len(r.Roles) == 0 {
		return false
	}
	for _, role := range r.Roles {
		if !role.Outcome.Success {
			return false
		}
	}
	return true
}
