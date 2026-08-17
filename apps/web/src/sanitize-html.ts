const allowedTags = new Set(["A", "ARTICLE", "B", "BLOCKQUOTE", "BR", "CODE", "DIV", "EM", "H1", "H2", "H3", "H4", "HR", "IMG", "LI", "OL", "P", "PRE", "SECTION", "SMALL", "SPAN", "STRONG", "UL"]);
const allowedAttributes = new Set(["alt", "class", "href", "src", "title"]);

export function sanitizeHTML(source: string): string {
  const document = new DOMParser().parseFromString(`<body>${source}</body>`, "text/html");
  for (const node of Array.from(document.body.querySelectorAll("*"))) {
    if (!allowedTags.has(node.tagName)) { node.replaceWith(...Array.from(node.childNodes)); continue; }
    for (const attribute of Array.from(node.attributes)) {
      if (!allowedAttributes.has(attribute.name.toLowerCase())) node.removeAttribute(attribute.name);
    }
    for (const name of ["href", "src"]) {
      const value = node.getAttribute(name)?.trim() || "";
      if (value && !(value.startsWith("/") || /^https?:/i.test(value))) node.removeAttribute(name);
    }
    if (node.tagName === "A") { node.setAttribute("rel", "noopener noreferrer"); }
  }
  return document.body.innerHTML;
}
