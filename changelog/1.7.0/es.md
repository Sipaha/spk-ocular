# SPK Ocular 1.7.0

- Las listas de Kubernetes cargan por completo el tipo solicitado y los namespaces seleccionados mediante cachés compactas compartidas, páginas pequeñas automáticas y watches. Un presupuesto de lectura por sesión y la retención limitada de cachés inactivas reducen las solicitudes innecesarias y la memoria retenida.
- Las consultas demasiado grandes de All Namespaces se detienen y solicitan elegir namespaces concretos, sin presentar una lista parcial como completa.
- Los iconos de actualización uniformes vuelven a leer las listas y giran hasta terminar. La carga muestra el tiempo transcurrido y los datos de inventario recibidos cuando están disponibles. Cancelar sustituye la lista por un aviso persistente; Reintentar o una nueva conexión vuelve a iniciar la carga.
- El grafo del clúster solo se carga tras un clic explícito en cada conexión. La selección, la búsqueda y la apertura de detalles conservan la cámara.
- Se han mejorado la selección de conexiones, las etiquetas de tipos, las ayudas de tablas, la navegación de la paleta de comandos dentro del ámbito elegido y la selección de logs. Volver a renderizar los puertos de un recurso ya no repite la misma consulta de información.
- Las mediciones locales de inventario y memoria, sus datos, metodología y límites están en `docs/performance.md`; no son garantías generales de rendimiento en producción.
- Las listas evitan decodificar JSON repetidamente y reducen el trabajo de CPU al cargar inventarios grandes de Kubernetes.
