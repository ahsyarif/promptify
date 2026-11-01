// Small progressive enhancements that htmx doesn't cover.

document.addEventListener("click", async (event) => {
  const button = event.target.closest("[data-copy]");
  if (!button) return;
  const source = document.querySelector(button.dataset.copy);
  if (!source) return;

  const text = source.textContent;
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    const range = document.createRange();
    range.selectNodeContents(source);
    const selection = window.getSelection();
    selection.removeAllRanges();
    selection.addRange(range);
    document.execCommand("copy");
    selection.removeAllRanges();
  }
  const label = button.textContent;
  button.textContent = "Copied";
  setTimeout(() => (button.textContent = label), 1500);
});

document.addEventListener("change", (event) => {
  const input = event.target.closest("input[type=file][data-preview]");
  if (!input) return;
  const preview = document.querySelector(input.dataset.preview);
  const file = input.files && input.files[0];
  if (preview.src) URL.revokeObjectURL(preview.src);
  if (file) {
    preview.src = URL.createObjectURL(file);
    preview.hidden = false;
  } else {
    preview.removeAttribute("src");
    preview.hidden = true;
  }
});

document.addEventListener("keydown", (event) => {
  if (event.key !== "/" || event.metaKey || event.ctrlKey) return;
  const target = event.target;
  if (target.closest("input, textarea, select, [contenteditable]")) return;
  const search = document.querySelector("[data-search]");
  if (!search) return;
  event.preventDefault();
  search.focus();
  search.select();
});
