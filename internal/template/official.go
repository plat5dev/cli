package template

// Official is the first-party API starters, in the order plat5 init offers them.
var Official = []OfficialTemplate{
	{Name: "go-fiber-api", Description: "Go + Fiber reference API (tasks inside a project, and the person's profile)"},
	{Name: "node-fastify-api", Description: "Node + Fastify + TypeBox reference API (tasks inside a project, and the person's profile)"},
	{Name: "bun-effect-api", Description: "Bun + Effect reference API (tasks inside a project, and the person's profile)"},
}

// CustomerConsole is the first-party customer console plat5 init adds beside an
// API starter.
var CustomerConsole = OfficialTemplate{Name: "customer-console", Description: "Vite + React console for your customers"}

// OperatorConsole is the first-party operator console plat5 init adds beside an
// API starter.
var OperatorConsole = OfficialTemplate{Name: "operator-console", Description: "Vite + React console for your operators"}

// OfficialTemplate is a first-party template: the repo plat5dev/template-<name>,
// or the folder template-<name> in a local templates directory.
type OfficialTemplate struct {
	Name        string
	Description string
}

// Repo is the template's repo on GitHub, owner/repo.
func (o OfficialTemplate) Repo() string { return "plat5dev/" + o.Folder() }

// Folder is the template's folder in a local templates directory: the repo's name.
func (o OfficialTemplate) Folder() string { return "template-" + o.Name }

// OfficialNames is the starters' names, for help and errors.
func OfficialNames() []string {
	out := make([]string, len(Official))
	for i, o := range Official {
		out[i] = o.Name
	}
	return out
}

func lookupOfficial(name string) (OfficialTemplate, bool) {
	for _, o := range Official {
		if o.Name == name {
			return o, true
		}
	}
	return OfficialTemplate{}, false
}
