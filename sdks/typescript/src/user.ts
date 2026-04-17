interface UserContext {
  id?: string;
  email?: string;
  username?: string;
}

let currentUser: UserContext | null = null;

export function setUser(user: UserContext): void {
  currentUser = { ...user };
}

export function clearUser(): void {
  currentUser = null;
}

export function getUser(): UserContext | null {
  return currentUser ? { ...currentUser } : null;
}
