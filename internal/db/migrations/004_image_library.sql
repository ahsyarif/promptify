-- This content reset intentionally cascades to bookmarks; accounts and payments remain.
DELETE FROM prompts;
DELETE FROM categories;

ALTER TABLE prompts
    ADD COLUMN model           TEXT NOT NULL,
    ADD COLUMN negative_prompt TEXT NOT NULL DEFAULT '',
    ADD COLUMN aspect_ratio    TEXT NOT NULL DEFAULT '',
    ADD CONSTRAINT prompts_image_required CHECK (image_path <> '');

INSERT INTO categories (name, slug, description) VALUES
    ('Photography',             'photography',           'Portraits, products, landscapes and street photos'),
    ('Illustration',            'illustration',          'Digital painting, line art, watercolour and comics'),
    ('3D & Render',             '3d-render',             '3D characters, objects, isometric scenes and renders'),
    ('Anime & Manga',           'anime-manga',           'Anime characters, scenes and manga panels'),
    ('Logo & Icon',             'logo-icon',             'Logos, icons, stickers and flat vector graphics'),
    ('Architecture & Interior', 'architecture-interior', 'Buildings, interiors and spatial design');
