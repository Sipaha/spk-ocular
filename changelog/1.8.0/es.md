# SPK Ocular 1.8.0

- Edite valores individuales de ConfigMap con resaltado de sintaxis, búsqueda y deshacer. La revisión muestra el cambio exacto respecto al valor actual; los datos binarios y el contenido de Secret conservan su presentación protegida.
- Busque recursos mediante fragmentos explícitos de namespace, tipo y nombre. Los namespaces ambiguos ofrecen una elección; una coincidencia actual única abre el objeto exacto sin perder la protección de cambios sin guardar.
- Los grupos de navegación tienen iconos y espaciado uniformes. Al contraer un grupo se ocultan todos sus elementos, incluido el recurso seleccionado, y se conserva la página abierta.
- Los espacios desconectados muestran una vista estática del proveedor, Connect/Cancel junto al estado e ilustraciones transparentes e independientes de un faro y montañas. El faro dibujado y su haz suave mantienen un tamaño fijo y se recortan al cambiar el ancho del panel lateral.
- Un único pie muestra el estado de la vista activa. Los detalles de actualización aparecen tras dos segundos sin desplazar los controles; el foco y el tamaño de paneles anidados no desplazan el marco de la aplicación.
- Alt+Shift+flechas cambia el tamaño del panel enfocado. Alt+Shift+Enter separa o devuelve los registros manteniendo un solo flujo; las nuevas ventanas aprovechan más el área de trabajo de la pantalla actual.
- Los menús del panel inferior cierran todas las pestañas o las situadas a la izquierda o derecha, respetando las pestañas fijadas y su pertenencia.
- Los detalles de recursos tienen una tipografía más clara y tablas compactas de revisiones y Events coherentes. Events conserva ordenación, ajuste de columnas y Refresh; las relaciones de Ingress con servicios muestran cada ruta declarada.
- Se evitó el reenvío de texto del terminal ya entregado al reemplazar texto mediante IME. Se protegen la composición activa, el pegado Unicode y los búferes de anuncios del lector de pantalla.
