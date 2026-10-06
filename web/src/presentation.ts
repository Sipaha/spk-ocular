import { getLanguage, t } from './i18n'
import type { KindDescriptor } from './api/types'
import type { Language } from './languages'

// Only known UI labels are localized; provider IDs, API kinds, custom groups,
// resource names and persistence keys retain the provider's original values.
const rows = [
'Address|Адрес|地址|Dirección|Adresse|Adresse|Endereço|アドレス',
'Age|Возраст|存在时间|Antigüedad|Alter|Âge|Idade|経過時間',
'Available|Доступно|可用|Disponibles|Verfügbar|Disponibles|Disponíveis|利用可能',
'Class|Класс|类别|Clase|Klasse|Classe|Classe|クラス',
'Config files|Файлы конфигурации|配置文件|Archivos de configuración|Konfigurationsdateien|Fichiers de configuration|Arquivos de configuração|設定ファイル',
'Containers|Контейнеры|容器|Contenedores|Container|Conteneurs|Contêineres|コンテナ',
'Count|Количество|数量|Cantidad|Anzahl|Nombre|Quantidade|数',
'Current|Текущее|当前|Actual|Aktuell|Actuel|Atual|現在',
'Desired|Желаемое|期望|Deseados|Gewünscht|Souhaité|Desejado|希望数',
'Driver|Драйвер|驱动程序|Controlador|Treiber|Pilote|Driver|ドライバー',
'Health|Работоспособность|健康状态|Estado de salud|Zustand|État de santé|Integridade|健全性',
'Hosts|Хосты|主机|Hosts|Hosts|Hôtes|Hosts|ホスト',
'Image|Образ|镜像|Imagen|Image|Image|Imagem|イメージ',
'Images|Образы|镜像|Imágenes|Images|Images|Imagens|イメージ',
'Keys|Ключи|键|Claves|Schlüssel|Clés|Chaves|キー',
'Last seen|Последнее наблюдение|上次观察时间|Última observación|Zuletzt gesehen|Dernière observation|Última observação|最終確認',
'Memory|Память|内存|Memoria|Arbeitsspeicher|Mémoire|Memória|メモリ',
'Message|Сообщение|消息|Mensaje|Nachricht|Message|Mensagem|メッセージ',
'Name|Имя|名称|Nombre|Name|Nom|Nome|名前',
'Namespace|Пространство имён|命名空间|Espacio de nombres|Namespace|Espace de noms|Namespace|名前空間',
'Node|Узел|节点|Nodo|Knoten|Nœud|Nó|ノード',
'Object|Объект|对象|Objeto|Objekt|Objet|Objeto|オブジェクト',
'Ports|Порты|端口|Puertos|Ports|Ports|Portas|ポート',
'Project|Проект|项目|Proyecto|Projekt|Projet|Projeto|プロジェクト',
'Ready|Готово|就绪|Listos|Bereit|Prêts|Prontos|準備完了',
'Reason|Причина|原因|Motivo|Grund|Raison|Motivo|理由',
'Restarts|Перезапуски|重启次数|Reinicios|Neustarts|Redémarrages|Reinicializações|再起動回数',
'Roles|Роли|角色|Roles|Rollen|Rôles|Funções|ロール',
'Running|Работают|运行中|En ejecución|Läuft|En cours|Em execução|実行中',
'Scope|Область|范围|Ámbito|Bereich|Périmètre|Escopo|範囲',
'Service|Сервис|服务|Servicio|Dienst|Service|Serviço|サービス',
'Services|Сервисы|服务|Servicios|Dienste|Services|Serviços|サービス',
'Size|Размер|大小|Tamaño|Größe|Taille|Tamanho|サイズ',
'Status|Статус|状态|Estado|Status|Statut|Status|状態',
'Tags|Теги|标签|Etiquetas|Tags|Tags|Tags|タグ',
'Type|Тип|类型|Tipo|Typ|Type|Tipo|種類',
'Up-to-date|Актуально|已更新|Actualizados|Aktuell|À jour|Atualizados|更新済み',
'Used by|Используют|使用者|Usado por|Verwendet von|Utilisé par|Usado por|使用元',
'Version|Версия|版本|Versión|Version|Version|Versão|バージョン',
'Working dir|Рабочая папка|工作目录|Directorio de trabajo|Arbeitsverzeichnis|Répertoire de travail|Diretório de trabalho|作業ディレクトリ',
'Workloads|Рабочие нагрузки|工作负载|Cargas de trabajo|Workloads|Charges de travail|Cargas de trabalho|ワークロード',
'Network|Сеть|网络|Red|Netzwerk|Réseau|Rede|ネットワーク',
'Config|Конфигурация|配置|Configuración|Konfiguration|Configuration|Configuração|設定',
'Cluster|Кластер|集群|Clúster|Cluster|Cluster|Cluster|クラスタ',
'Storage|Хранилища|存储|Almacenamiento|Speicher|Stockage|Armazenamento|ストレージ',
'Access Control|Управление доступом|访问控制|Control de acceso|Zugriffssteuerung|Contrôle d’accès|Controle de acesso|アクセス制御',
'API groups|API-группы|API 组|Grupos de API|API-Gruppen|Groupes d’API|Grupos de API|API グループ',
'Engine|Движок Docker|Docker 引擎|Motor de Docker|Docker Engine|Moteur Docker|Docker Engine|Docker エンジン',
'Projects|Проекты|项目|Proyectos|Projekte|Projets|Projetos|プロジェクト',
'Networks|Сети|网络|Redes|Netzwerke|Réseaux|Redes|ネットワーク',
'Volumes|Тома|卷|Volúmenes|Volumes|Volumes|Volumes|ボリューム',
'Problems|Проблемы|问题|Problemas|Probleme|Problèmes|Problemas|問題',
] as const
const order: Language[] = ['en', 'ru', 'zh', 'es', 'de', 'fr', 'pt', 'ja']
export const presentationLabels = Object.fromEntries(order.map((language, index) => [language, Object.fromEntries(rows.map(row => {
  const parts = row.split('|')
  if (parts.length !== order.length) throw new Error('Incomplete presentation labels')
  return [parts[0], parts[index]]
}))])) as Record<Language, Record<string, string>>

export function columnLabel(title: string, language: Language = getLanguage()): string {
  const labels = presentationLabels[language]
  return Object.hasOwn(labels, title) ? labels[title] : title
}
export function groupLabel(provider: string, title: string, language: Language = getLanguage()): string {
  return provider === 'kubernetes' || provider === 'compose' ? columnLabel(title, language) : title
}
export function kindLabel(provider: string, kind: KindDescriptor): string {
  if (provider === 'kubernetes') {
    if (kind.id === 'ocular.helm.releases') return t('helm.releases')
    if (kind.id === 'ocular.helm.charts') return t('helm.charts')
    if (kind.id === 'problems') return columnLabel(kind.title)
  }
  return provider === 'compose' ? columnLabel(kind.title) : kind.title
}
