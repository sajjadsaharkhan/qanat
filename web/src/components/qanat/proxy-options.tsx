import { SelectGroup, SelectItem, SelectLabel, SelectSeparator } from "@/components/ui/select";
import type { Schemas } from "@/lib/api/client";

/** Label of what "inherit" falls through to: the global default proxy, else direct (PRD §8.2). */
export function defaultLabel(proxies: Schemas["Proxy"][]) {
  return proxies.find((p) => p.is_default)?.label ?? "direct";
}

/** Human label for a proxy setting value. */
export function choiceLabel(value: string, proxies: Schemas["Proxy"][], groups: Schemas["ProxyGroup"][]) {
  if (value === "inherit" || value === "direct") return value === "direct" ? "Direct" : "Inherit";
  if (value.startsWith("group:")) return groups.find((g) => `group:${g.id}` === value)?.label ?? "Unknown group";
  return proxies.find((p) => p.id === value)?.label ?? "Unknown proxy";
}

/** Select items for a proxy setting: "inherit", "direct", a proxy ID, or "group:<id>" (PRD §8.2). */
export function ProxyChoiceOptions({
  proxies,
  groups,
  inherit,
}: {
  proxies: Schemas["Proxy"][];
  groups: Schemas["ProxyGroup"][];
  /** What "inherit" resolves to here, shown in brackets. */
  inherit: string;
}) {
  return (
    <>
      <SelectItem value="inherit">Inherit ({inherit})</SelectItem>
      <SelectItem value="direct">Direct</SelectItem>
      {proxies.length > 0 && (
        <>
          <SelectSeparator />
          <SelectGroup>
            <SelectLabel>Proxies</SelectLabel>
            {proxies.map((p) => (
              <SelectItem key={p.id} value={p.id}>
                {p.label}
                {!p.enabled && " (disabled)"}
              </SelectItem>
            ))}
          </SelectGroup>
        </>
      )}
      {groups.length > 0 && (
        <>
          <SelectSeparator />
          <SelectGroup>
            <SelectLabel>Groups</SelectLabel>
            {groups.map((g) => (
              <SelectItem key={g.id} value={`group:${g.id}`}>
                {g.label} ({g.policy})
              </SelectItem>
            ))}
          </SelectGroup>
        </>
      )}
    </>
  );
}
