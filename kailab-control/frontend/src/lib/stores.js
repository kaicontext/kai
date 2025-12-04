import { writable } from 'svelte/store';

// Auth stores - tokens are now stored in HttpOnly cookies (not accessible from JS)
// We only track the current user in memory
export const currentUser = writable(null);

// Navigation state
export const currentOrg = writable(null);
export const currentRepo = writable(null);
