<!-- Code generated from the comments of the ImageConfig struct in builder/openstack/image_config.go; DO NOT EDIT MANUALLY -->

-   `metadata` (map[string]string) - Glance metadata that will be applied to the image.
    
-   `image_visibility` (imageservice.ImageVisibility) - One of "public", "private", "shared", or "community".
    
-   `image_members` ([]string) - List of members to add to the image after creation. An image member is
    usually a project (also called the "tenant") with whom the image is
    shared.
    
-   `image_disk_format` (string) - Disk format of the resulting image. This option works if
    use_blockstorage_volume is true.
    
-   `image_tags` ([]string) - List of tags to add to the image after creation.
    
-   `image_min_disk` (int) - Minimum disk size needed to boot image, in gigabytes.
    