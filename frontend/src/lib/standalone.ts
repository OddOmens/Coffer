/**
 * Whether the app was opened from the Home Screen (or installed on a desktop)
 * rather than in a browser tab: there's no address bar, no tabs and no Back
 * button around it, so nothing may lead somewhere it can't come back from.
 */
export const standalone =
  typeof window !== "undefined" &&
  (window.matchMedia("(display-mode: standalone)").matches || (navigator as Navigator & { standalone?: boolean }).standalone === true)
