package metadata

type QueryFilter struct {
	Engine          string
	CollectorID     string
	EnvironmentSlug string
	TargetNames     []string
}

type DatabaseTarget struct {
	ID              string
	EnvironmentSlug string
	Name            string
	Engine          string
	Host            string
	Port            int
	DatabaseName    string
	CredentialRef   string
}

type ProbeDefinition struct {
	ID            string
	Name          string
	Category      string
	QueryTemplate string
	TimeoutMS     int
}

type ProbeAssignment struct {
	ID              string
	IntervalSeconds int
}

type ScheduledProbe struct {
	CollectorID string
	Target      DatabaseTarget
	Definition  ProbeDefinition
	Assignment  ProbeAssignment
}

type Evidence struct {
	Kind     string
	Title    string
	Metadata map[string]string
}
