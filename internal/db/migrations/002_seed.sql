INSERT INTO categories (name, slug, description) VALUES
    ('Writing',      'writing',      'Copywriting, editing and storytelling'),
    ('Coding',       'coding',       'Programming, debugging and code review'),
    ('Image',        'image',        'Prompts for image generation models'),
    ('Marketing',    'marketing',    'Campaigns, SEO and social media'),
    ('Productivity', 'productivity', 'Planning, summarising and learning');

INSERT INTO prompts (title, description, body, category_id) VALUES
(
    'Concise editor',
    'Tighten any draft without losing its voice.',
    E'You are a meticulous editor. Rewrite the text below so it is clear and concise.\n\n- Keep the author''s voice and intent\n- Remove filler words and redundancy\n- Prefer active voice\n- Return only the revised text\n\nText:\n"""\n{{text}}\n"""',
    (SELECT id FROM categories WHERE slug = 'writing')
),
(
    'Short story opener',
    'Generate a gripping first paragraph for a story.',
    E'Write the opening paragraph of a {{genre}} short story set in {{setting}}. Start in the middle of the action, introduce one vivid sensory detail, and end on a question the reader needs answered. Maximum 120 words.',
    (SELECT id FROM categories WHERE slug = 'writing')
),
(
    'Senior code reviewer',
    'Thorough review focused on correctness and security.',
    E'Act as a senior {{language}} engineer reviewing a pull request. For the code below, list:\n\n1. Bugs or incorrect behaviour\n2. Security issues\n3. Performance concerns\n4. Readability improvements\n\nFor each item, quote the relevant line and propose a concrete fix. Skip praise and nitpicks.\n\n```\n{{code}}\n```',
    (SELECT id FROM categories WHERE slug = 'coding')
),
(
    'Explain this error',
    'Turn a cryptic stack trace into a fix.',
    E'I got the following error while running {{command}}:\n\n```\n{{error}}\n```\n\nExplain in plain language what caused it, then give the most likely fix as a step-by-step list. If more context is needed, tell me exactly which file or output to share.',
    (SELECT id FROM categories WHERE slug = 'coding')
),
(
    'SQL query builder',
    'Describe the data you need, get a query back.',
    E'Given this PostgreSQL schema:\n\n{{schema}}\n\nWrite a single SQL query that {{goal}}. Use CTEs for readability, explain any index that would speed it up, and point out edge cases such as NULLs or duplicates.',
    (SELECT id FROM categories WHERE slug = 'coding')
),
(
    'Cinematic product shot',
    'Studio-quality product photography prompt.',
    E'Professional product photograph of {{product}} on a seamless {{color}} backdrop, soft diffused key light from the left, subtle rim light, shallow depth of field, 85mm lens, f/2.8, ultra detailed, commercial advertising style --ar 4:5',
    (SELECT id FROM categories WHERE slug = 'image')
),
(
    'Minimal line-art illustration',
    'Clean single-line drawings for icons and posters.',
    E'Minimalist continuous single-line drawing of {{subject}}, black ink on off-white paper, generous negative space, elegant and balanced composition, no shading, no text',
    (SELECT id FROM categories WHERE slug = 'image')
),
(
    'Landing page headline variants',
    'Ten headline options tuned to a specific audience.',
    E'Write 10 landing page headlines for {{product}}, aimed at {{audience}}. Mix these angles: outcome, pain point, curiosity, social proof and urgency. Keep each under 10 words and label the angle used.',
    (SELECT id FROM categories WHERE slug = 'marketing')
),
(
    'SEO meta description',
    'Click-worthy meta descriptions within length limits.',
    E'Write three meta descriptions for a page titled "{{title}}" targeting the keyword "{{keyword}}". Each must be 140–155 characters, include the keyword naturally and end with a soft call to action.',
    (SELECT id FROM categories WHERE slug = 'marketing')
),
(
    'Meeting notes to action items',
    'Extract decisions and owners from messy notes.',
    E'From the meeting notes below, produce:\n\n- **Decisions** made\n- **Action items** as a table with columns: task, owner, due date\n- **Open questions**\n\nIf an owner or date is missing, write "TBD".\n\nNotes:\n{{notes}}',
    (SELECT id FROM categories WHERE slug = 'productivity')
),
(
    'Feynman tutor',
    'Learn any topic by explaining it simply.',
    E'Teach me {{topic}} using the Feynman technique. Explain it as if I were 12, using one everyday analogy. Then ask me three questions to check my understanding, one at a time, and wait for my answer before continuing.',
    (SELECT id FROM categories WHERE slug = 'productivity')
);
