CREATE TABLE categories (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    slug        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE prompts (
    id          BIGSERIAL PRIMARY KEY,
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    body        TEXT NOT NULL,
    category_id BIGINT REFERENCES categories(id) ON DELETE SET NULL,
    image_path  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    search      TSVECTOR GENERATED ALWAYS AS (
        setweight(to_tsvector('english', title), 'A') ||
        setweight(to_tsvector('english', description), 'B') ||
        setweight(to_tsvector('english', body), 'C')
    ) STORED
);

CREATE INDEX prompts_search_idx ON prompts USING GIN (search);
CREATE INDEX prompts_category_idx ON prompts (category_id);
CREATE INDEX prompts_created_idx ON prompts (created_at DESC, id DESC);

CREATE TABLE bookmarks (
    visitor_id TEXT NOT NULL,
    prompt_id  BIGINT NOT NULL REFERENCES prompts(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (visitor_id, prompt_id)
);

CREATE INDEX bookmarks_prompt_idx ON bookmarks (prompt_id);
