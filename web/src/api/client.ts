import type {
  CreateReservationInput,
  CreateTableInput,
  DateOnly,
  Reservation,
  ReservationFilters,
  ServiceHours,
  Table,
  TableAvailability,
  UpdateTableInput,
  UUID,
  Window,
} from '@/types/api'

const BASE = import.meta.env.VITE_API_URL ?? 'http://localhost:8080'

/**
 * O erro da API, com o status HTTP preservado.
 *
 * A v1 da API não tem `error.code` machine-readable — é débito técnico 1 da spec,
 * assumido, e a migração para código estruturado será breaking change. Então o
 * ÚNICO dado estruturado que o cliente tem é o **status**:
 *
 *   400 → o staff digitou algo inválido (a mensagem diz o quê)
 *   404 → sumiu
 *   409 → conflito: mesa ocupada, ou sem disponibilidade no horário
 *   500 → bug nosso; a mensagem é sempre "Erro interno." e não serve para o staff
 *
 * `message` vem do corpo `{"error": "..."}` e é escrita em português, para humano.
 * A UI EXIBE essa string — nunca tenta interpretá-la. Fazer `if (msg.includes(
 * "capacidade"))` seria construir um parser de linguagem natural sobre um contrato
 * que a spec avisou que vai mudar.
 */
export class ApiError extends Error {
  // Campo declarado e atribuído à mão, e não `constructor(readonly status: ...)`.
  // O scaffold liga `erasableSyntaxOnly`, que proíbe parameter properties: elas
  // são açúcar que NÃO some quando você apaga os tipos, e a flag existe para
  // garantir que todo TS deste projeto seja JS válido com os tipos removidos.
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }

  /** Conflito de agenda: 409. O único erro que o staff resolve mudando a escolha. */
  get isConflict(): boolean {
    return this.status === 409
  }

  /** Bug nosso. A mensagem não ajuda o staff; a UI deve dizer isso com honestidade. */
  get isServerFault(): boolean {
    return this.status >= 500
  }
}

/** A rede caiu, o backend não subiu, o CORS recusou. Não é erro de domínio. */
export class NetworkError extends Error {
  constructor(cause: unknown) {
    super('Não foi possível falar com o servidor.')
    this.name = 'NetworkError'
    this.cause = cause
  }
}

type Method = 'GET' | 'POST' | 'PATCH' | 'DELETE'

/**
 * O wrapper existe para UMA coisa: garantir que `{"error": "..."}` vire ApiError
 * num lugar só. Sem ele, cada componente faria `if (!res.ok)` do seu jeito, e o
 * dia em que a API ganhar `error.code` (breaking change previsto na spec) teria
 * que ser consertado em vinte lugares. Aqui é um.
 *
 * Não é Axios porque não há nada aqui que o fetch não faça. Uma dependência a
 * menos é uma dependência a menos.
 */
async function request<T>(method: Method, path: string, body?: unknown): Promise<T> {
  let res: Response

  try {
    res = await fetch(`${BASE}${path}`, {
      method,
      headers: body ? { 'Content-Type': 'application/json' } : {},
      body: body ? JSON.stringify(body) : undefined,
    })
  } catch (causa) {
    // fetch só rejeita por falha de REDE (ou CORS). Um 500 do servidor resolve
    // normalmente — por isso a checagem de status vem depois, fora do catch.
    throw new NetworkError(causa)
  }

  if (!res.ok) {
    throw new ApiError(res.status, await mensagemDeErro(res))
  }

  // 204 No Content: o DELETE /reservations/{id} responde assim. `res.json()` num
  // corpo vazio lança SyntaxError — um erro de parse mascarando um sucesso.
  if (res.status === 204) {
    return undefined as T
  }

  return (await res.json()) as T
}

/**
 * Extrai a mensagem de `{"error": "..."}`, com dois fallbacks que importam:
 * um 500 pode vir com corpo vazio ou com um HTML de proxy, e nesse caso o
 * `res.json()` explode. O staff nunca pode ver um SyntaxError na tela por causa
 * disso.
 */
async function mensagemDeErro(res: Response): Promise<string> {
  try {
    const corpo = (await res.json()) as { error?: string }
    if (corpo.error) return corpo.error
  } catch {
    // corpo vazio ou não-JSON: cai no genérico abaixo
  }
  return `O servidor respondeu ${res.status}.`
}

/** Monta ?a=1&b=2 pulando os filtros ausentes. Um filtro `undefined` não vai. */
function query(params: Record<string, string | undefined>): string {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== '') q.set(k, v)
  }
  const s = q.toString()
  return s ? `?${s}` : ''
}

export const api = {
  tables: {
    list: (active?: boolean) =>
      request<Table[]>('GET', `/tables${query({ active: active?.toString() })}`),

    get: (id: UUID) => request<Table>('GET', `/tables/${id}`),

    create: (input: CreateTableInput) => request<Table>('POST', '/tables', input),

    /**
     * Também é o "excluir" deste domínio: não existe DELETE de mesa na API, e isso
     * é decisão, não lacuna — apagar a mesa levaria junto o histórico de reservas
     * que apontam para ela. Desativar é `update({ is_active: false })`, e NÃO existe
     * um atalho `deactivate()` aqui de propósito: dois caminhos para a mesma
     * chamada é o tipo de coisa que alguém acha em seis meses e usa sem saber que o
     * outro existe.
     */
    update: (id: UUID, input: UpdateTableInput) =>
      request<Table>('PATCH', `/tables/${id}`, input),

    availability: (id: UUID, date: DateOnly) =>
      request<Window[]>('GET', `/tables/${id}/availability${query({ date })}`),
  },

  reservations: {
    list: (f: ReservationFilters = {}) =>
      request<Reservation[]>('GET', `/reservations${query({ ...f })}`),

    get: (id: UUID) => request<Reservation>('GET', `/reservations/${id}`),

    create: (input: CreateReservationInput) =>
      request<Reservation>('POST', '/reservations', input),

    /**
     * Edita (remarca): cancela a reserva atual e cria uma nova, no servidor, numa
     * transação só. A Reservation devolvida tem ID NOVO — editar não muta a linha.
     */
    update: (id: UUID, input: CreateReservationInput) =>
      request<Reservation>('PATCH', `/reservations/${id}`, input),

    /** 204, sem corpo. Idempotente: cancelar duas vezes devolve 204 nas duas. */
    cancel: (id: UUID) => request<void>('DELETE', `/reservations/${id}`),
  },

  /** A grade do salão inteiro no dia. Duas queries no backend, uma requisição aqui. */
  availability: (date: DateOnly) =>
    request<TableAvailability[]>('GET', `/availability${query({ date })}`),

  serviceHours: () => request<ServiceHours>('GET', '/service-hours'),
}
