import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { api } from '@/api/client'
import { mensagemLegivel } from '@/api/errors'
import type { CreateTableInput, Table, UpdateTableInput, UUID } from '@/types/api'

export type FiltroMesas = 'todas' | 'ativas' | 'inativas'

export const useTablesStore = defineStore('tables', () => {
  const mesas = ref<Table[]>([])
  const carregando = ref(false)
  const filtro = ref<FiltroMesas>('todas')

  /**
   * O erro de CARREGAMENTO, e só ele.
   *
   * Repare no que este store NÃO faz: ele não guarda o erro de criar nem o de
   * editar. Esses são jogados de volta (`throw`) para quem chamou.
   *
   * A razão é que os dois tipos de erro têm DONOS diferentes na tela: falhar ao
   * listar é um estado da PÁGINA (não há o que mostrar), enquanto "já existe uma
   * mesa chamada Mesa 12" (409) é um estado do FORMULÁRIO — precisa aparecer
   * colado no campo que causou o problema, com o modal ainda aberto e o que o
   * staff digitou ainda lá. Um `erro` global no store forçaria o formulário a
   * ficar vigiando uma variável que não é dele, e um segundo erro em outra tela
   * apagaria o primeiro.
   */
  const erroDeCarga = ref<string | null>(null)

  const visiveis = computed(() => {
    switch (filtro.value) {
      case 'ativas':
        return mesas.value.filter((m) => m.is_active)
      case 'inativas':
        return mesas.value.filter((m) => !m.is_active)
      default:
        return mesas.value
    }
  })

  const totalAtivas = computed(() => mesas.value.filter((m) => m.is_active).length)

  /**
   * Lugares das mesas ATIVAS. A soma das inativas seria uma capacidade que o
   * restaurante não tem — o número no topo da tela precisa ser o que a casa senta
   * hoje, não o que ela sentaria se tudo estivesse de pé.
   */
  const lugaresAtivos = computed(() =>
    mesas.value.reduce((soma, m) => (m.is_active ? soma + m.capacity : soma), 0),
  )

  async function carregar() {
    carregando.value = true
    erroDeCarga.value = null

    try {
      // Sempre lista TODAS as mesas, sem o ?active= da API. O filtro é do olho, não
      // do servidor: são dezenas de mesas, não milhares, e trocar de aba não deve
      // custar um round-trip nem um estado de "carregando" piscando na tela.
      mesas.value = await api.tables.list()
    } catch (e) {
      erroDeCarga.value = mensagemLegivel(e)
      mesas.value = []
    } finally {
      carregando.value = false
    }
  }

  /** Devolve a mesa criada; LANÇA ApiError em 409 (nome duplicado) e 400. */
  async function criar(input: CreateTableInput): Promise<Table> {
    const nova = await api.tables.create(input)
    mesas.value.push(nova)
    return nova
  }

  /** Devolve a mesa atualizada; LANÇA ApiError em 409, 404 e 400. */
  async function atualizar(id: UUID, input: UpdateTableInput): Promise<Table> {
    const atualizada = await api.tables.update(id, input)
    substituir(atualizada)
    return atualizada
  }

  /**
   * Desativar é o "excluir" deste domínio — não existe DELETE de mesa na API, e
   * isso é decisão, não lacuna: apagar a mesa levaria junto o histórico de
   * reservas que apontam para ela.
   */
  async function alternarAtiva(mesa: Table): Promise<Table> {
    const atualizada = await api.tables.update(mesa.id, { is_active: !mesa.is_active })
    substituir(atualizada)
    return atualizada
  }

  function substituir(mesa: Table) {
    const i = mesas.value.findIndex((m) => m.id === mesa.id)
    if (i !== -1) mesas.value[i] = mesa
  }

  return {
    mesas,
    carregando,
    erroDeCarga,
    filtro,
    visiveis,
    totalAtivas,
    lugaresAtivos,
    carregar,
    criar,
    atualizar,
    alternarAtiva,
  }
})
