import type { BreadcrumbItem } from "@wick-fe/common-ui";

const TRUNCATE_THRESHOLD = 20;

export function buildSkillFileCrumbs(
  folder: string,
  file: string,
  navigate: (path: string) => void
): BreadcrumbItem[] {
  const items: BreadcrumbItem[] = [
    { label: "Skills", onClick: () => navigate("/") },
    { label: folder, onClick: () => navigate(`/skills/${encodeURIComponent(folder)}`) },
  ];

  const segments = file.split("/");
  segments.forEach((seg, i) => {
    const isLast = i === segments.length - 1;
    const cumulative = segments
      .slice(0, i + 1)
      .map(encodeURIComponent)
      .join("/");

    const item: BreadcrumbItem = { label: seg };
    if (seg.length > TRUNCATE_THRESHOLD) {
      item.truncate = true;
    }
    if (!isLast) {
      item.onClick = () => navigate(`/skills/${encodeURIComponent(folder)}/files/${cumulative}`);
    }
    items.push(item);
  });

  return items;
}
