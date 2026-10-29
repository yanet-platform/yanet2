use proc_macro2::TokenStream;
use quote::{ToTokens, format_ident, quote, quote_spanned};
use syn::{
    Data, DeriveInput, Fields,
    spanned::Spanned,
    visit_mut::{self, VisitMut},
};

struct StorageSelf<'a>(&'a syn::Ident);
impl VisitMut for StorageSelf<'_> {
    fn visit_type_path_mut(&mut self, ty: &mut syn::TypePath) {
        if ty.qself.is_none() && ty.path.is_ident("Self") {
            let name = self.0;
            ty.path = syn::parse_quote!(#name);
        }
        visit_mut::visit_type_path_mut(self, ty);
    }
}

/// Requires C layout with optional alignment; expansion checks the allocator
/// limit.
fn check_repr(input: &DeriveInput) -> syn::Result<()> {
    let mut repr_c = false;
    for attr in input.attrs.iter().filter(|a| a.path().is_ident("repr")) {
        attr.parse_nested_meta(|meta| {
            if meta.path.is_ident("C") {
                repr_c = true;
                Ok(())
            } else if meta.path.is_ident("align") {
                let content;
                syn::parenthesized!(content in meta.input);
                content.parse::<syn::LitInt>()?;
                Ok(())
            } else if meta.path.is_ident("packed") {
                Err(meta.error("ShmLayout rejects repr(packed): fields could be misaligned"))
            } else {
                Err(meta.error("ShmLayout supports only repr(C), optionally with align"))
            }
        })?;
    }
    if repr_c {
        Ok(())
    } else {
        Err(syn::Error::new(input.ident.span(), "ShmLayout requires #[repr(C)]"))
    }
}

pub fn expand(input: &DeriveInput) -> syn::Result<TokenStream> {
    let name = &input.ident;
    if !input.generics.params.is_empty() || input.generics.where_clause.is_some() {
        return Err(syn::Error::new(
            input.generics.span(),
            "ShmLayout cannot be derived for a generic type",
        ));
    }
    let Data::Struct(data) = &input.data else {
        return Err(syn::Error::new(
            name.span(),
            "ShmLayout can only be derived for a struct",
        ));
    };
    let Fields::Named(fields) = &data.fields else {
        return Err(syn::Error::new(name.span(), "ShmLayout requires named fields"));
    };
    if fields.named.is_empty() {
        return Err(syn::Error::new(
            name.span(),
            "ShmLayout requires at least one field; use `()` for an empty configuration",
        ));
    }
    check_repr(input)?;
    let sdk = format_ident!("__yanet_sdk_for_{}", name);
    let core = format_ident!("__yanet_core_for_{}", name);
    let p = quote!(#sdk::__private);
    let source = input.to_token_stream().to_string();
    let fresh = |stem: &str| {
        let mut candidate = stem.to_owned();
        while source.contains(&candidate) {
            candidate.push('_');
        }
        format_ident!("{candidate}")
    };
    let owner = fresh("__YanetOwner");
    let error = fresh("__YanetError");
    let initializer = format_ident!("{}Init", name);
    let visibility = &input.vis;
    let mut parameters = Vec::new();
    let mut initializer_fields = Vec::new();
    let mut encoder_bounds = Vec::new();
    let mut writes = Vec::new();
    let mut guards = Vec::new();
    let mut bounds = Vec::new();
    let mut sizes = Vec::new();
    let mut frees = Vec::new();
    for (index, field) in fields.named.iter().enumerate() {
        let ident = &field.ident;
        let mut ty = field.ty.clone();
        StorageSelf(name).visit_type_mut(&mut ty);
        let parameter = fresh(&format!("__YanetField{index}"));
        let guard = fresh(&format!("__yanet_guard_{index}"));
        let field_visibility = &field.vis;
        initializer_fields.push(quote!(#field_visibility #ident: #parameter));
        encoder_bounds.push(quote_spanned!(ty.span()=> #parameter: #p::Encode<#error, Output = #ty>));
        writes.push(quote_spanned! {field.span()=>
            let #guard = __writer.field(self.#ident, &raw mut (*__destination).#ident)?;
        });
        parameters.push(parameter);
        guards.push(guard);
        bounds.push(quote_spanned!(ty.span()=> __field_is_stored::<#ty>();));
        sizes.push(quote!(+ #core::mem::size_of::<#ty>()));
        frees.push(quote_spanned! {ty.span()=>
            <#ty as #p::Stored>::free::<#owner>(
                __addr + #core::mem::offset_of!(#name, #ident), __memory,
            );
        });
    }
    frees.reverse();
    let padding = format!("ShmLayout: `{name}` has implicit padding; add explicit padding fields");
    let drop = format!("ShmLayout: `{name}` needs drop; shared-memory types are never dropped");
    let align = format!("ShmLayout: `{name}` is aligned to more than the block allocator's MAX_ALIGN");
    Ok(quote! {
        #[allow(non_snake_case)]
        extern crate yanet_sdk as #sdk;
        #[allow(non_snake_case)]
        extern crate core as #core;
        const _: () = {
            fn __field_is_stored<T: #p::Stored>() {}
            fn __fields() { #(#bounds)* }
            #core::assert!(#core::mem::size_of::<#name>() == 0 #(#sizes)*, #padding);
            #core::assert!(!#core::mem::needs_drop::<#name>(), #drop);
            #core::assert!(#core::mem::align_of::<#name>() <= #p::MAX_ALIGN, #align);
        };

        /// Direct initializer for the complete shared-memory record.
        #visibility struct #initializer<#(#parameters),*> {
            #(#initializer_fields),*
        }

        // SAFETY: exact field bounds cover the whole concrete storage record.
        //
        // Only raw pointers project incomplete storage. Each completed field
        // stays guarded until all fields succeed and the record owns them.
        unsafe impl<#error, #(#parameters),*> #p::Encode<#error> for #initializer<#(#parameters),*>
        where #(#encoder_bounds),*
        {
            type Output = #name;

            unsafe fn encode<#owner>(
                self,
                __writer: &mut #p::Writer<'_, #owner>,
                __destination: *mut Self::Output,
            ) -> #core::result::Result<(), #error>
            where
                #owner: #p::Owner,
                #error: #core::convert::From<#p::BuildError<<#owner as #p::Owner>::Error>>,
            {
                // SAFETY: the caller supplies the exclusive final record.
                unsafe { #(#writes)* #(#core::mem::forget(#guards);)* }
                #core::result::Result::Ok(())
            }
        }

        // SAFETY: the checked concrete C layout accepts every bit pattern.
        //
        // Every field satisfies the shared-memory contract. The struct is
        // unpacked, within the allocator's alignment, free of drop glue and
        // padding, as checked against the actual dependencies above.
        unsafe impl #p::ShmLayout for #name {}

        // SAFETY: each complete field owns exactly its own descendants.
        //
        // Release visits every field once in reverse declaration order, using
        // the same offsets as construction and the field's release contract.
        unsafe impl #p::Stored for #name {
            unsafe fn free<#owner: #p::Owner>(
                __addr: usize,
                __memory: &#p::MemoryContext<'_, #owner>,
            ) {
                // SAFETY: the caller supplies the entire complete value.
                unsafe { #(#frees)* }
            }
        }
    })
}
