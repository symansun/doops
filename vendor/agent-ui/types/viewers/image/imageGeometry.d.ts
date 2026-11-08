export interface ImagePoint {
    x: number;
    y: number;
}
export interface ImageDimensions {
    width: number;
    height: number;
}
export declare function normalizePoint(point: ImagePoint, dimensions: ImageDimensions): ImagePoint;
export declare function denormalizePoint(point: ImagePoint, dimensions: ImageDimensions): ImagePoint;
