import type { Reservation, UUID } from '@/types/api'

/**
 * O estado operacional de uma mesa num dado instante.
 *
 *   inativa     → fora de operação (is_active = false). Precede tudo.
 *   ocupada     → tem gente AGORA (uma reserva confirmada em curso)
 *   reservada   → livre agora, mas tem reserva mais tarde no dia visto
 *   disponivel  → sem compromisso no dia visto
 */
export type StatusMesa = 'inativa' | 'ocupada' | 'reservada' | 'disponivel'

/**
 * Deriva o status de uma mesa a partir das reservas do dia visto e de um instante
 * de referência (`agoraMs`).
 *
 * Uma decisão que evita um monte de caso especial: `agoraMs` é sempre o AGORA real,
 * mesmo quando o dia visto não é hoje. Isso faz a função se comportar certo nos três
 * casos sem um `if (éHoje)`:
 *
 *   - dia = hoje    → agora cai no meio das reservas → distingue ocupada de reservada
 *   - dia = futuro  → agora é anterior a todas → nada "ocupada", tudo vira reservada
 *   - dia = passado → agora é posterior a todas → nada pendente, tudo disponivel
 *
 * `reservas` deve vir já filtrada para as CONFIRMADAS do dia visto (a agenda e o
 * salão já carregam só essas).
 */
export function statusMesa(
  reservas: Reservation[],
  tableID: UUID,
  ativa: boolean,
  agoraMs: number,
): StatusMesa {
  if (!ativa) return 'inativa'

  let temFutura = false

  for (const r of reservas) {
    if (!r.table_ids.includes(tableID)) continue

    const ini = new Date(r.starts_at).getTime()
    const fim = new Date(r.ends_at).getTime()

    // Em curso: [) como a tstzrange do banco — o instante do fim já libera a mesa.
    if (ini <= agoraMs && agoraMs < fim) return 'ocupada'
    if (ini > agoraMs) temFutura = true
  }

  return temFutura ? 'reservada' : 'disponivel'
}

/**
 * Rótulo e cores de cada status, num lugar só para os dois lugares que exibem.
 *
 * `texto` e `ponto` são classes LITERAIS de propósito — nunca construídas com
 * `.replace('text-','bg-')`. O scanner do Tailwind só gera as classes que encontra
 * escritas por extenso no código; uma classe montada em runtime não existe no CSS,
 * e o elemento fica sem cor, em silêncio.
 */
export const APARENCIA_STATUS: Record<
  StatusMesa,
  { rotulo: string; texto: string; ponto: string }
> = {
  ocupada: { rotulo: 'Ocupada', texto: 'text-blood-300', ponto: 'bg-blood-300' },
  reservada: { rotulo: 'Reservada', texto: 'text-ember-400', ponto: 'bg-ember-400' },
  disponivel: { rotulo: 'Disponível', texto: 'text-sage-500', ponto: 'bg-sage-500' },
  inativa: { rotulo: 'Fora', texto: 'text-ink-500', ponto: 'bg-ink-500' },
}
