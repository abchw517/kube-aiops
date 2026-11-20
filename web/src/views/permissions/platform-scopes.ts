export type Scope = { environment: string; cluster?: string; namespace?: string; business?: string };

const scopeKey = (scope: Scope): string => JSON.stringify(
  [scope.environment, scope.cluster ?? "", scope.business ?? "", scope.namespace ?? ""],
);

export function scopeDifference(before: Scope[], after: Scope[]) {
  const previous = new Set(before.map(scopeKey));
  const next = new Set(after.map(scopeKey));
  return {
    added: [...new Map(after.map((scope) => [scopeKey(scope), scope])).entries()]
      .filter(([key]) => !previous.has(key)).map(([, scope]) => scope),
    removed: before.filter((scope) => !next.has(scopeKey(scope))),
  };
}

export function roleReferences(id: string | undefined, users: {
  roles: string[] | null; groups: string[] | null;
}[], groups: { id: string; roles: string[] | null }[]) {
  const inherited = new Set(groups.filter((group) => id && group.roles?.includes(id)).map((group) => group.id));
  const direct = users.filter((user) => id && user.roles?.includes(id));
  const affected = new Set([...direct, ...users.filter((user) => user.groups?.some((group) => inherited.has(group)))]);
  return { direct: direct.length, groups: inherited.size, affected: affected.size };
}
