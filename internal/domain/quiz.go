package domain

// QuizOption представляет один вариант ответа в блиц-квизе
type QuizOption struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// QuizQuestion представляет вопрос с несколькими вариантами ответов (Multiple Choice)
type QuizQuestion struct {
	ID            int          `json:"id"`
	TopicID       string       `json:"topic_id"`
	TopicTitle    string       `json:"topic_title,omitempty"`
	Difficulty    Difficulty   `json:"difficulty"`
	Question      string       `json:"question"`
	Options       []QuizOption `json:"options"`
	CorrectOption string       `json:"correct_option"`
	Explanation   string       `json:"explanation"`
}

