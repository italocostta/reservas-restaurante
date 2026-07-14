import type { DateOnly, Timestamp } from '@/types/api'

/**
 * Toda conversão entre "20:00" (o que o staff digita e lê) e um INSTANTE (o que a
 * API guarda) passa por aqui. É o único módulo do frontend autorizado a construir
 * um Date a partir de texto.
 *
 * A REGRA, e ela é absoluta: o fuso da verdade é o do RESTAURANTE, que vem do
 * GET /service-hours. NUNCA o do navegador.
 *
 * Por que isso importa mais aqui do que parece:
 *
 *   new Date('2026-08-01T20:00')   // interpretado no fuso do NAVEGADOR
 *
 * Se o laptop do gerente estiver com fuso errado — ou se alguém abrir isto de
 * outro estado — a reserva entra deslocada, e o número que aparece na tela vem
 * deslocado JUNTO, de forma consistente. O erro fica invisível de dentro: a tela
 * concorda com ela mesma e discorda do banco.
 *
 * É a mesma armadilha que o backend evita com ParseInLocation em vez de Parse
 * (seção 4 da spec) e com AT TIME ZONE no filtro ?date=. A spec a chama de "a
 * mesma armadilha, sempre disfarçada". Reintroduzi-la no cliente seria a ironia
 * perfeita.
 */

/** Minutos desde a meia-noite. "18:30" → 1110. */
export function hhmmParaMinutos(hhmm: string): number {
  const [h, m] = hhmm.split(':').map(Number)
  return h * 60 + m
}

/** O inverso. 1110 → "18:30". */
export function minutosParaHHMM(minutos: number): string {
  const h = Math.floor(minutos / 60)
  const m = minutos % 60
  return `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`
}

/**
 * O offset do fuso `tz` NAQUELE instante, em minutos. São Paulo hoje devolve -180.
 *
 * "Naquele instante" não é preciosismo: o offset de um fuso NÃO é constante — ele
 * muda no horário de verão. O Brasil não tem DST hoje, mas já teve e pode voltar,
 * e o dia em que voltar este código não pode ser o que quebra. Cravar -03:00 numa
 * constante seria escrever um bug com data de validade.
 */
function offsetEm(instante: Date, tz: string): number {
  const partes = new Intl.DateTimeFormat('en-US', {
    timeZone: tz,
    timeZoneName: 'longOffset',
  }).formatToParts(instante)

  const nome = partes.find((p) => p.type === 'timeZoneName')?.value ?? 'GMT'

  // "GMT-03:00" → -180. "GMT" puro (UTC) → 0.
  const m = /GMT([+-])(\d{2}):(\d{2})/.exec(nome)
  if (!m) return 0

  const sinal = m[1] === '-' ? -1 : 1
  return sinal * (Number(m[2]) * 60 + Number(m[3]))
}

/**
 * "2026-08-01" + "20:00" + "America/Sao_Paulo" → o instante ISO correspondente às
 * 20h DAQUELE dia NAQUELE fuso.
 *
 * O algoritmo tem um passo que parece paranoia e não é. Para saber o offset do
 * fuso preciso de um instante; para achar o instante preciso do offset. A saída é
 * chutar (interpretando a hora como se fosse UTC), medir o offset ali, corrigir —
 * e então MEDIR DE NOVO no instante corrigido.
 *
 * A segunda medição existe porque a correção pode atravessar uma fronteira de
 * horário de verão: o chute cai de um lado, o instante real do outro, e os dois
 * têm offsets diferentes. Sem a reconferência, a reserva marcada para as 20h de um
 * domingo de virada entraria às 19h ou às 21h.
 */
export function instanteDe(dia: DateOnly, hhmm: string, tz: string): Timestamp {
  const chute = new Date(`${dia}T${hhmm}:00Z`)

  const offset1 = offsetEm(chute, tz)
  const candidato = new Date(chute.getTime() - offset1 * 60_000)

  const offset2 = offsetEm(candidato, tz)
  const real =
    offset2 === offset1 ? candidato : new Date(chute.getTime() - offset2 * 60_000)

  return real.toISOString()
}

/** O instante da API → a hora de PAREDE do restaurante. "20:00". */
export function horaLocal(instante: Timestamp, tz: string): string {
  return new Intl.DateTimeFormat('pt-BR', {
    timeZone: tz,
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(instante))
}

/**
 * Minutos desde a meia-noite DO RESTAURANTE. É o que posiciona uma barra na grade:
 * a régua do dia vai de SERVICE_START a SERVICE_END, e cada janela vira um offset
 * em pixels a partir daí.
 *
 * Pode passar de 1440 (24h): a reserva que começa 22h30 e termina 00h30 do dia
 * seguinte devolve 1470 no fim — o que é DESEJADO. A validação 8 da spec permite
 * `ends_at` ultrapassar o fechamento (é a última mesa do dia), e a barra dela
 * precisa continuar crescendo para a direita, não voltar para o começo da régua.
 */
export function minutosNoDia(instante: Timestamp, tz: string, dia: DateOnly): number {
  const d = new Date(instante)

  const meiaNoite = new Date(instanteDe(dia, '00:00', tz))
  const decorridos = (d.getTime() - meiaNoite.getTime()) / 60_000

  return Math.round(decorridos)
}

/** Hoje, no fuso do restaurante — e não no do navegador. "2026-07-14". */
export function hojeNoRestaurante(tz: string): DateOnly {
  // 'en-CA' porque o formato dele já é AAAA-MM-DD. Usar 'pt-BR' daria 14/07/2026,
  // e eu teria que desmontar e remontar a string para chegar no mesmo lugar.
  return new Intl.DateTimeFormat('en-CA', { timeZone: tz }).format(new Date())
}

/** "2026-07-14" → "ter, 14 jul". Para o cabeçalho da agenda. */
export function diaPorExtenso(dia: DateOnly, tz: string): string {
  return new Intl.DateTimeFormat('pt-BR', {
    timeZone: tz,
    weekday: 'short',
    day: '2-digit',
    month: 'short',
  }).format(new Date(instanteDe(dia, '12:00', tz)))
  // 12:00 e não 00:00: o meio-dia está longe de qualquer fronteira de fuso ou de
  // horário de verão. À meia-noite, um deslocamento de uma hora muda o DIA.
}
