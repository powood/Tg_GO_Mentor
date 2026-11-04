-- 1. Таблица пользователей
CREATE TABLE IF NOT EXISTS users (
    id BIGINT PRIMARY KEY,                 -- Telegram User ID
    username VARCHAR(255),                 -- @username в Telegram
    first_name VARCHAR(255) NOT NULL,      -- Имя пользователя
    target_grade VARCHAR(32) NOT NULL DEFAULT 'all', -- Целевой грейд (all, junior, middle, senior)
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 2. Таблица тем
CREATE TABLE IF NOT EXISTS topics (
    id VARCHAR(64) PRIMARY KEY CHECK (octet_length(id) <= 48), -- Лимит под callback_data <= 64 байт
    title VARCHAR(255) NOT NULL,           -- Человеческое название
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 3. Банк вопросов
CREATE TABLE IF NOT EXISTS questions (
    id SERIAL PRIMARY KEY,
    topic_id VARCHAR(64) NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    difficulty VARCHAR(32) NOT NULL DEFAULT 'middle',
    text TEXT NOT NULL,                    -- Текст вопроса
    answer TEXT NOT NULL,                  -- Эталонный ответ
    key_points TEXT[] NOT NULL DEFAULT '{}', -- Массив ключевых тезисов
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Индекс для быстрой выборки всех вопросов темы
CREATE INDEX IF NOT EXISTS idx_questions_topic_id ON questions(topic_id);

-- 4. Личный прогресс пользователя по каждому вопросу (для нашей адаптивной колоды)
CREATE TABLE IF NOT EXISTS user_progress (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    question_id INT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    topic_id VARCHAR(64) NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    attempts INT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    success_count INT NOT NULL DEFAULT 0 CHECK (success_count >= 0),
    last_score INT NOT NULL DEFAULT 0 CHECK (last_score BETWEEN 0 AND 10),
    last_answer_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, question_id)
);

-- Индекс для мгновенного построения колоды пользователя по конкретной теме
CREATE INDEX IF NOT EXISTS idx_user_progress_user_topic ON user_progress(user_id, topic_id);

-- 5. История всех попыток ответов (логирование и аналитика)
CREATE TABLE IF NOT EXISTS answer_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    question_id INT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    user_answer TEXT NOT NULL,
    is_voice BOOLEAN NOT NULL DEFAULT FALSE,
    score INT NOT NULL CHECK (score BETWEEN 1 AND 10),
    is_correct BOOLEAN NOT NULL,
    feedback TEXT NOT NULL,
    missing_points TEXT[] NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_attempts_user_id ON answer_attempts(user_id, created_at DESC);