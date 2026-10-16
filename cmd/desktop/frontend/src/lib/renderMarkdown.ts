import DOMPurify from 'dompurify';
import { Marked } from 'marked';

function escapeHTML(value: string) {
  return value.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
}

const parser = new Marked({
  gfm: true,
  breaks: false,
  renderer: {
    html({ text }) {
      return escapeHTML(text);
    },
  },
});

export function renderMarkdown(source: string): string {
  return DOMPurify.sanitize(parser.parse(source) as string, { USE_PROFILES: { html: true } });
}
