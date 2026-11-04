package domain

type Difficulty string

const (
	DifficultyIntern Difficulty = "intern"
	DifficultyJunior Difficulty = "junior"
	DifficultyMiddle Difficulty = "middle"
	DifficultySenior Difficulty = "senior"
)

type Topic struct {
	ID          string
	Title       string
	Description string
}

type Question struct {
	ID         int
	TopicID    string
	Difficulty Difficulty
	Text       string
	Answer     string
	KeyPoints  []string
}
