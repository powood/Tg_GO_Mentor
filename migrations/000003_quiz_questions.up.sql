-- 000003_quiz_questions.up.sql
-- Таблица вопросов для режима «Блиц-квиз» (Multiple Choice Questions)
CREATE TABLE IF NOT EXISTS quiz_questions (
    id SERIAL PRIMARY KEY,
    topic_id VARCHAR(64) NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    difficulty VARCHAR(32) NOT NULL DEFAULT 'junior', -- 'intern', 'junior', 'middle', 'senior'
    question TEXT NOT NULL,                           -- Текст вопроса и сниппет кода
    options JSONB NOT NULL DEFAULT '[]',              -- Массив вариантов: [{"id": "A", "text": "..."}, {"id": "B", "text": "..."}, {"id": "C", "text": "..."}, {"id": "D", "text": "..."}]
    correct_option VARCHAR(8) NOT NULL DEFAULT '',    -- Правильный вариант ("A", "B", "C", "D") — заполняется отдельным скриптом верификации
    explanation TEXT NOT NULL DEFAULT '',              -- Пояснение правильного ответа — заполняется отдельным скриптом
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Индексы для мгновенной выборки вопросов нужной сложности по выбранной теме
CREATE INDEX IF NOT EXISTS idx_quiz_questions_topic_diff ON quiz_questions(topic_id, difficulty);
CREATE INDEX IF NOT EXISTS idx_quiz_questions_diff ON quiz_questions(difficulty);

