// Tipos do domínio, escritos à MÃO — não gerados do swagger.json.
//
// É o débito técnico 16 da spec, e ele é real: nada aqui impede este arquivo de
// divergir do Go. Se alguém renomear um campo no backend, o `vue-tsc` vai
// concordar alegremente com a versão velha, porque para ele a verdade é ESTE
// arquivo. O teste de contrato (internal/openapitest) protege o servidor contra
// a documentação mentir; não existe nada protegendo o cliente contra ISTO aqui
// mentir.
//
// Aceito porque gerar acoplaria o build do front ao `swag init` para 4 tipos.
// Registrado porque um débito não-escrito é só um bug com atraso.
//
// A referência canônica é o docs/swagger.json na raiz do repo. Ao mexer aqui,
// confira lá.

/** Timestamp ISO 8601 com offset. Ex: "2026-08-01T19:00:00-03:00" */
export type Timestamp = string

/** Dia no fuso do restaurante, AAAA-MM-DD. Nunca um Date. */
export type DateOnly = string

export type UUID = string

// ---------- Mesas ----------

export interface Table {
  id: UUID
  name: string
  capacity: number
  is_active: boolean
  created_at: Timestamp
}

export interface CreateTableInput {
  name: string
  capacity: number
}

// Patch parcial: campo ausente = "não mexer". Espelha o updateRequest do Go, que
// usa ponteiro em todo campo pela mesma razão — `is_active: false` precisa ser
// distinguível de "não mencionei is_active".
//
// Em TypeScript o `?` já dá isso de graça, MAS só se ninguém mandar `undefined`
// explícito: JSON.stringify remove chaves undefined, então o efeito é o mesmo.
export interface UpdateTableInput {
  name?: string
  capacity?: number
  is_active?: boolean
}

// ---------- Reservas ----------

export type ReservationStatus = 'confirmed' | 'cancelled'

export interface Reservation {
  id: UUID
  /**
   * LISTA desde a Fase 3a (seção 14 da spec), não um id único.
   *
   *   [] / ausente → o sistema escolheu (heurística automática)
   *   1 mesa       → override manual
   *   2+ mesas     → COMBINAÇÃO: o staff empurrou as mesas
   */
  table_ids: UUID[]
  customer_name: string
  customer_phone: string
  party_size: number
  starts_at: Timestamp
  ends_at: Timestamp
  status: ReservationStatus
  created_at: Timestamp
}

export interface CreateReservationInput {
  /** Omitir/vazio = "escolha a mesa por mim". Ver os três significados acima. */
  table_ids?: UUID[]
  customer_name: string
  customer_phone: string
  party_size: number
  starts_at: Timestamp
  ends_at: Timestamp
}

export interface ReservationFilters {
  date?: DateOnly
  table_id?: UUID
  status?: ReservationStatus
}

// ---------- Disponibilidade e expediente (seção 15) ----------

/** Intervalo [starts_at, ends_at) — fim exclusivo, igual à tstzrange do banco. */
export interface Window {
  starts_at: Timestamp
  ends_at: Timestamp
}

/** Uma linha da grade do dia: a mesa, e quando ela está livre. */
export interface TableAvailability {
  table_id: UUID
  table_name: string
  capacity: number
  free_windows: Window[]
}

/**
 * Expediente do restaurante, vindo de GET /service-hours.
 *
 * NÃO duplicar isto num .env do Vue. O endpoint existe exatamente para que esta
 * verdade viva num lugar só — ver seção 15 da spec.
 */
export interface ServiceHours {
  /** "18:00" */
  start: string
  /** "23:00" */
  end: string
  /** "America/Sao_Paulo" */
  tz: string
}
