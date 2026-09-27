/** The score ring's color step (OC-018 AC1): zinc < 40%, indigo 40–80%, emerald > 80%. */
export function scoreColor(ratio: number): 'low' | 'mid' | 'high' {
  if (ratio > 0.8) return 'high'
  if (ratio >= 0.4) return 'mid'
  return 'low'
}
