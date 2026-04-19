package entity

type HealthState string

const (
	HealthStateOK   HealthState = "ok"
	HealthStateFail HealthState = "fail"
)

type HealthComponent struct {
	Database HealthState `json:"database"`
}
