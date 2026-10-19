package types

const DefaultApplicationTarget = 5
const MaxApplicationTarget = 100
const DefaultVacancyLimit = 5
const MaxVacancyLimit = 100
const DefaultMinimumScore = 7
const MaxMatchScore = 10

type Job struct {
	Title       string `json:"title"`
	Company     string `json:"company"`
	URL         string `json:"url"`
	Snippet     string `json:"snippet"`
	Applied     bool   `json:"applied"`
	ApplyResult string `json:"apply_result,omitempty"`
	MatchScore  int    `json:"match_score,omitempty"`
}

type Config struct {
	Headless          bool
	Debug             bool
	UseAI             bool
	AutoApply         bool
	ProfileDir        string
	ModelURL          string
	ModelName         string
	AreaID            int
	MaxJobs           int
	MinimumScore      int
	ApplicationTarget int
	ResumePath        string
}

type Resume struct {
	Name         string        `json:"name"`
	Title        string        `json:"title"`
	Experience   []Experience  `json:"experience"`
	Projects     []Project     `json:"projects"`
	Achievements []Achievement `json:"achievements"`
	Skills       []string      `json:"skills"`
	Education    []Education   `json:"education"`
	Languages    []Language    `json:"languages"`
	Summary      string        `json:"summary"`
	ContactInfo  Contact       `json:"contact"`
}

type Achievement struct {
	Name      string `json:"name"`
	Result    string `json:"result"`
	Date      string `json:"date"`
	Organizer string `json:"organizer"`
	Link      string `json:"link"`
}

type Project struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Technologies []string `json:"technologies"`
	Link         string   `json:"link"`
}

type Experience struct {
	Position         string   `json:"position"`
	Company          string   `json:"company"`
	Period           string   `json:"period"`
	Responsibilities []string `json:"responsibilities"`
}

type Education struct {
	Degree     string `json:"degree"`
	University string `json:"university"`
	Year       string `json:"year"`
}

type Language struct {
	Language string `json:"language"`
	Level    string `json:"level"`
}

type Contact struct {
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	LinkedIn string `json:"linkedin"`
	GitHub   string `json:"github"`
}

type AgentAction struct {
	Action     string                 `json:"action"`
	Parameters map[string]interface{} `json:"parameters"`
	Reason     string                 `json:"reason"`
	Result     string                 `json:"result,omitempty"`
	Timestamp  string                 `json:"timestamp"`
}

func GetJob(title, company, url, snippet string) Job {
	return Job{
		Title:   title,
		Company: company,
		URL:     url,
		Snippet: snippet,
	}
}
