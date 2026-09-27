import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

/** Merges Tailwind class names, letting later classes win. */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}
