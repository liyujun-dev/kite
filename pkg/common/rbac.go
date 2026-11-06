package common

type Verb string

const (
	VerbGet    Verb = "get"
	VerbCreate Verb = "create"
	VerbUpdate Verb = "update"
	VerbDelete Verb = "delete"
	VerbLog    Verb = "log"
	VerbExec   Verb = "exec"
)

type Role struct {
	Name        string   `yaml:"name" json:"name"`
	Description string   `yaml:"description" json:"-"`
	Clusters    []string `yaml:"clusters" json:"clusters"`
	Resources   []string `yaml:"resources" json:"resources"`
	Namespaces  []string `yaml:"namespaces" json:"namespaces"`
	Verbs       []string `yaml:"verbs" json:"verbs"`
}

type RoleMapping struct {
	Name       string   `yaml:"name" json:"name"`
	Users      []string `yaml:"users,omitempty" json:"users,omitempty"`
	OIDCGroups []string `yaml:"oidcGroups,omitempty" json:"oidcGroups,omitempty"`
}

type RolesConfig struct {
	Roles       []Role        `yaml:"roles"`
	RoleMapping []RoleMapping `yaml:"roleMapping"`

	// DefaultRoles maps an OAuth provider name to the role used when a user
	// matches no explicit role mapping. Built from the database at sync time.
	DefaultRoles map[string]string `yaml:"-"`
}
