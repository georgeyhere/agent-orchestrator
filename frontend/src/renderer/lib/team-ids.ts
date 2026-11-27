// Team records are keyed by short IDs the backend validates as
// [a-z][a-z0-9_-]{0,63}. People name things; the UI derives the ID.

const MAX_ID = 64;

/** Lowercase slug from a human name, or "" when the name has no letters or digits. */
export function slugify(name: string, prefix = "x"): string {
	const base = name
		.normalize("NFKD")
		.replace(/[̀-ͯ]/g, "")
		.toLowerCase()
		.replace(/[^a-z0-9]+/g, "-")
		.replace(/^-+|-+$/g, "");
	if (!base) return "";
	const lead = /^[a-z]/.test(base) ? base : `${prefix}-${base}`;
	return lead.slice(0, MAX_ID).replace(/-+$/, "");
}

/** A slug for `name` that no record in `taken` already uses. */
export function uniqueId(name: string, taken: Iterable<string>, prefix = "x"): string {
	const base = slugify(name, prefix);
	if (!base) return "";
	const used = new Set(taken);
	if (!used.has(base)) return base;
	for (let n = 2; ; n += 1) {
		const suffix = `-${n}`;
		const candidate = `${base.slice(0, MAX_ID - suffix.length).replace(/-+$/, "")}${suffix}`;
		if (!used.has(candidate)) return candidate;
	}
}
