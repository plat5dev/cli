package template

// Official first-party starters (hardcoded). Community templates: owner/repo or URL.
var Official = []OfficialTemplate{
	{
		Name:        "bun-effect-api",
		Repo:        "plat5dev/template-bun-effect-api",
		Description: "Bun + Effect reference API (profiles, projects, tasks)",
	},
	{
		Name:        "go-fiber-api",
		Repo:        "plat5dev/template-go-fiber-api",
		Description: "Go + Fiber reference API (profiles, projects, tasks)",
	},
	{
		Name:        "node-fastify-api",
		Repo:        "plat5dev/template-node-fastify-api",
		Description: "Node + Fastify + TypeBox reference API (profiles, projects, tasks)",
	},
}

// OfficialTemplate is a first-party starter listed by --list-templates.
type OfficialTemplate struct {
	Name        string
	Repo        string // owner/repo
	Description string
}

// OfficialNames returns short names for error messages.
func OfficialNames() []string {
	out := make([]string, len(Official))
	for i, o := range Official {
		out[i] = o.Name
	}
	return out
}

func lookupOfficial(name string) *OfficialTemplate {
	for i := range Official {
		if Official[i].Name == name {
			return &Official[i]
		}
	}
	return nil
}
