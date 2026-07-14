import { describe, expect, it } from 'vitest'

import { statusMesa } from './statusMesa'
import type { Reservation, UUID } from '@/types/api'

const MESA: UUID = 'mesa-a'
const OUTRA: UUID = 'mesa-b'

function reserva(mesas: UUID[], iniISO: string, fimISO: string): Reservation {
  return {
    id: crypto.randomUUID(),
    table_ids: mesas,
    customer_name: 'X',
    customer_phone: '0',
    party_size: 2,
    starts_at: iniISO,
    ends_at: fimISO,
    status: 'confirmed',
    created_at: iniISO,
  }
}

// Instantes fixos, em UTC, para não depender do relógio da máquina.
const AGORA = Date.parse('2026-08-01T23:00:00Z') // "agora"
const antes = '2026-08-01T22:00:00Z'
const emCurso1 = '2026-08-01T22:30:00Z'
const emCurso2 = '2026-08-01T23:30:00Z'
const depois1 = '2026-08-02T00:00:00Z'
const depois2 = '2026-08-02T01:00:00Z'

describe('statusMesa', () => {
  it('mesa inativa é sempre inativa, mesmo com reserva em curso', () => {
    const r = [reserva([MESA], emCurso1, emCurso2)]
    expect(statusMesa(r, MESA, false, AGORA)).toBe('inativa')
  })

  it('ocupada: reserva em curso agora', () => {
    const r = [reserva([MESA], emCurso1, emCurso2)]
    expect(statusMesa(r, MESA, true, AGORA)).toBe('ocupada')
  })

  it('reservada: só reserva futura, nada em curso', () => {
    const r = [reserva([MESA], depois1, depois2)]
    expect(statusMesa(r, MESA, true, AGORA)).toBe('reservada')
  })

  it('disponivel: só reserva já encerrada', () => {
    const r = [reserva([MESA], antes, '2026-08-01T22:45:00Z')]
    expect(statusMesa(r, MESA, true, AGORA)).toBe('disponivel')
  })

  it('disponivel: nenhuma reserva', () => {
    expect(statusMesa([], MESA, true, AGORA)).toBe('disponivel')
  })

  it('ocupada VENCE reservada quando há uma de cada', () => {
    // A mesa tem uma em curso E uma mais tarde. O estado mais urgente ganha.
    const r = [reserva([MESA], emCurso1, emCurso2), reserva([MESA], depois1, depois2)]
    expect(statusMesa(r, MESA, true, AGORA)).toBe('ocupada')
  })

  it('ignora reservas de OUTRAS mesas', () => {
    const r = [reserva([OUTRA], emCurso1, emCurso2)]
    expect(statusMesa(r, MESA, true, AGORA)).toBe('disponivel')
  })

  it('conta a COMBINAÇÃO: a mesa é metade de uma reserva de duas mesas', () => {
    // A ocupação de uma mesa combinada vale para as duas — table_ids inclui as duas.
    const r = [reserva([MESA, OUTRA], emCurso1, emCurso2)]
    expect(statusMesa(r, MESA, true, AGORA)).toBe('ocupada')
    expect(statusMesa(r, OUTRA, true, AGORA)).toBe('ocupada')
  })

  it('limite [): o instante exato do fim já libera a mesa', () => {
    // Reserva termina exatamente AGORA → não está mais em curso.
    const r = [reserva([MESA], emCurso1, '2026-08-01T23:00:00Z')]
    expect(statusMesa(r, MESA, true, AGORA)).toBe('disponivel')
  })
})
