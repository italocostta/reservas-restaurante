import { ApiError, NetworkError } from './client'

/**
 * Erro (de qualquer origem) → texto que o staff lê. É a ÚNICA tradução de erro do
 * frontend, e ela é deliberadamente burra: a mensagem do ApiError é exibida como
 * veio, nunca interpretada. A v1 da API não tem `error.code` (débito 1 da spec), e
 * construir um `if (msg.includes('capacidade'))` seria escrever um parser de
 * linguagem natural sobre um contrato que a própria spec avisa que vai mudar.
 *
 * A exceção é o 5xx: a API responde sempre "Erro interno.", que é honesto para o
 * log e inútil para quem está com fila na porta. Só aí o cliente escreve por conta
 * própria — porque a mensagem do servidor não tem nada a dizer.
 *
 * Mora AQUI, e não no store de mesas onde nasceu: ela não sabe o que é uma mesa.
 * A tela de agenda vai precisar dela, e importá-la de `stores/tables` seria um
 * acoplamento absurdo entre duas telas que não têm nada a ver uma com a outra.
 */
export function mensagemLegivel(e: unknown): string {
  if (e instanceof NetworkError) {
    return 'Sem resposta do servidor. Confira se a API está no ar (localhost:8080).'
  }
  if (e instanceof ApiError) {
    return e.isServerFault
      ? 'O servidor falhou. Tente de novo; se insistir, é bug nosso.'
      : e.message
  }
  return 'Algo inesperado aconteceu.'
}
